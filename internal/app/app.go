// Package app monta os componentes e controla o ciclo de vida do processo.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"gonginx-observer/internal/alerts"
	"gonginx-observer/internal/api"
	"gonginx-observer/internal/config"
	"gonginx-observer/internal/metrics"
	"gonginx-observer/internal/pipeline"
	"gonginx-observer/internal/tailer"
)

// Run inicia pipeline, motor de alertas e servidor HTTP e bloqueia até ctx
// ser cancelado (SIGINT/SIGTERM em produção). Em seguida executa o
// desligamento gracioso: o HTTP para de aceitar conexões, o pipeline drena
// o que já foi lido e todas as goroutines terminam.
//
// ready, se não nulo, recebe o endereço efetivo do listener (útil em testes
// com porta ":0").
func Run(ctx context.Context, cfg config.Config, version string, log *slog.Logger, ready func(addr string)) error {
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}

	store := metrics.New(metrics.Config{WindowSeconds: cfg.WindowSeconds, MaxKeys: cfg.MaxTrackedKeys})
	tl := tailer.New(tailer.Config{
		Path:         cfg.LogPath,
		PollInterval: cfg.PollInterval,
		MaxLineBytes: cfg.MaxLineBytes,
		FromStart:    cfg.ReadFromStart,
		Logger:       log.With("component", "tailer"),
	})
	pl := pipeline.New(tl, store, pipeline.Config{
		Workers:        cfg.Workers,
		RawQueueSize:   cfg.RawQueueSize,
		EventQueueSize: cfg.EventQueueSize,
		Logger:         log.With("component", "pipeline"),
	})
	eng := alerts.New(store, alerts.Config{
		IPRequestThreshold: cfg.AlertIPThreshold,
		ServerErrorRate:    cfg.Alert5xxRate,
		MinSamples:         cfg.AlertMinSamples,
		Cooldown:           cfg.AlertCooldown,
		Interval:           cfg.AlertEvalInterval,
		MaxAlerts:          cfg.AlertHistory,
		Logger:             log.With("component", "alerts"),
	})
	srv := &http.Server{
		Handler: api.New(api.Deps{
			Store: store, Alerts: eng, Pipeline: pl, Tailer: tl,
			Mask: cfg.MaskIPs, Version: version, Logger: log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	// Contexto interno: um erro fatal em qualquer componente encerra todos.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := pl.Run(ctx); err != nil {
			cancel(fmt.Errorf("pipeline: %w", err))
		}
		log.Info("pipeline encerrado")
	})
	wg.Go(func() {
		eng.Run(ctx)
		log.Info("motor de alertas encerrado")
	})
	httpErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if !errors.Is(err, http.ErrServerClosed) {
			cancel(fmt.Errorf("http: %w", err))
		}
		httpErr <- nil
	}()

	log.Info("observer iniciado", "addr", ln.Addr().String(), "log_path", cfg.LogPath,
		"workers", cfg.Workers, "version", version)
	if ready != nil {
		ready(ln.Addr().String())
	}

	<-ctx.Done()
	log.Info("desligando", "cause", context.Cause(ctx))

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown HTTP incompleto", "err", err)
	}
	<-httpErr

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		return errors.New("timeout aguardando goroutines no desligamento")
	}

	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	log.Info("desligamento concluído")
	return nil
}
