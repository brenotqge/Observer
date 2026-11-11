// Comando observer: serviço sidecar de observabilidade para o NGINX.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gonginx-observer/internal/app"
	"gonginx-observer/internal/config"
)

var version = "dev"

func main() {
	healthcheck := flag.Bool("healthcheck", false, "consulta /healthz local e sai com 0 (saudável) ou 1")
	showVersion := flag.Bool("version", false, "exibe a versão")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Uso: observer [-healthcheck] [-version]\n\nVariáveis de ambiente:\n")
		for _, v := range config.Vars {
			fmt.Fprintf(os.Stderr, "  %-28s padrão %-20q %s\n", v.Name, v.Default, v.Help)
		}
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := config.FromEnv(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuração inválida:\n"+err.Error())
		os.Exit(2)
	}

	if *healthcheck {
		os.Exit(runHealthcheck(cfg.ListenAddr))
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	// SIGTERM é enviado pelo Docker em `docker compose stop/down`.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, version, log, nil); err != nil {
		log.Error("encerrado com erro", "err", err)
		os.Exit(1)
	}
}

// runHealthcheck permite HEALTHCHECK em imagem sem shell/curl (distroless).
func runHealthcheck(listen string) int {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 1
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
