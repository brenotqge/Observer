// Package alerts implementa regras heurísticas simples de detecção de
// anomalias sobre as métricas em janela.
//
// IMPORTANTE: alertas são indicadores, não provas. Um pico de requisições
// por IP pode ser um proxy corporativo, um crawler legítimo ou um teste de
// carga; um aumento de 5xx pode ser um deploy. Nenhuma ação automática
// (como bloqueio de IP) é executada.
package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gonginx-observer/internal/metrics"
)

// Tipos de alerta.
const (
	TypeIPRequestRate = "ip_request_rate"
	TypeServerErrors  = "server_error_rate"
)

// Alert é um evento de detecção.
type Alert struct {
	ID            int64     `json:"id"`
	Type          string    `json:"type"`
	DetectedAt    time.Time `json:"detected_at"`
	Entity        string    `json:"entity"`
	Metric        string    `json:"metric"`
	Observed      float64   `json:"observed"`
	Threshold     float64   `json:"threshold"`
	WindowSeconds int64     `json:"window_seconds"`
	Description   string    `json:"description"`
}

// Source é o que o motor precisa das métricas (facilita testes).
type Source interface {
	WindowIPCounts() map[string]int64
	WindowSummary() metrics.Window
}

// Config parametriza as regras.
type Config struct {
	// Regra A: requisições por IP na janela acima deste valor geram alerta.
	IPRequestThreshold int64
	// Regra B: proporção de 5xx (0..1) acima deste valor, com pelo menos
	// MinSamples requisições na janela.
	ServerErrorRate float64
	MinSamples      int64
	// Cooldown evita alertas repetidos para a mesma regra/entidade.
	Cooldown  time.Duration
	Interval  time.Duration
	MaxAlerts int
	Now       func() time.Time
	Logger    *slog.Logger
}

// Engine avalia as regras periodicamente.
type Engine struct {
	cfg Config
	src Source

	mu       sync.RWMutex
	alerts   []Alert // ring buffer em ordem de inserção
	nextID   int64
	total    int64
	lastFire map[string]time.Time
}

// New cria o motor.
func New(src Source, cfg Config) *Engine {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.MaxAlerts <= 0 {
		cfg.MaxAlerts = 100
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Engine{cfg: cfg, src: src, lastFire: make(map[string]time.Time)}
}

// Config retorna a configuração efetiva (para exibição na API).
func (e *Engine) Config() Config { return e.cfg }

// Run avalia as regras a cada Interval até ctx ser cancelado.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(e.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Evaluate()
		}
	}
}

// Evaluate executa uma rodada de avaliação e retorna os novos alertas.
func (e *Engine) Evaluate() []Alert {
	now := e.cfg.Now()
	var fired []Alert

	if th := e.cfg.IPRequestThreshold; th > 0 {
		for ip, n := range e.src.WindowIPCounts() {
			if n > th {
				fired = e.maybeFire(fired, now, Alert{
					Type:      TypeIPRequestRate,
					Entity:    ip,
					Metric:    "requests_per_ip_window",
					Observed:  float64(n),
					Threshold: float64(th),
					Description: fmt.Sprintf(
						"IP realizou %d requisições na janela, acima do limite de %d. "+
							"Indicador heurístico: pode ser tráfego legítimo (proxy, crawler, teste).", n, th),
				})
			}
		}
	}

	if th := e.cfg.ServerErrorRate; th > 0 {
		w := e.src.WindowSummary()
		if w.Requests >= e.cfg.MinSamples && w.ServerErrorRate > th {
			fired = e.maybeFire(fired, now, Alert{
				Type:      TypeServerErrors,
				Entity:    "global",
				Metric:    "server_error_rate_window",
				Observed:  w.ServerErrorRate,
				Threshold: th,
				Description: fmt.Sprintf(
					"%.1f%% das respostas na janela foram 5xx (%d de %d), acima do limite de %.1f%%.",
					w.ServerErrorRate*100, w.Errors5xx, w.Requests, th*100),
			})
		}
	}
	return fired
}

func (e *Engine) maybeFire(fired []Alert, now time.Time, a Alert) []Alert {
	key := a.Type + "|" + a.Entity
	e.mu.Lock()
	defer e.mu.Unlock()
	if last, ok := e.lastFire[key]; ok && now.Sub(last) < e.cfg.Cooldown {
		return fired
	}
	e.lastFire[key] = now
	// Limpa chaves antigas para não crescer indefinidamente.
	if len(e.lastFire) > 10*e.cfg.MaxAlerts {
		for k, t := range e.lastFire {
			if now.Sub(t) >= e.cfg.Cooldown {
				delete(e.lastFire, k)
			}
		}
	}
	e.nextID++
	e.total++
	a.ID = e.nextID
	a.DetectedAt = now
	a.WindowSeconds = e.src.WindowSummary().Seconds
	if len(e.alerts) >= e.cfg.MaxAlerts {
		e.alerts = append(e.alerts[:0], e.alerts[1:]...)
	}
	e.alerts = append(e.alerts, a)
	e.cfg.Logger.Warn("alerta", "type", a.Type, "metric", a.Metric, "observed", a.Observed, "threshold", a.Threshold)
	return append(fired, a)
}

// Recent retorna até n alertas, do mais recente para o mais antigo, e o
// total desde a inicialização.
func (e *Engine) Recent(n int) ([]Alert, int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if n < 0 || n > len(e.alerts) {
		n = len(e.alerts)
	}
	out := make([]Alert, 0, n)
	for i := len(e.alerts) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, e.alerts[i])
	}
	return out, e.total
}
