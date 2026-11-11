// Package config lê a configuração a partir de variáveis de ambiente.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gonginx-observer/internal/ipmask"
)

// Config é a configuração completa do Observer.
type Config struct {
	ListenAddr     string
	LogPath        string
	ReadFromStart  bool
	PollInterval   time.Duration
	MaxLineBytes   int
	Workers        int
	RawQueueSize   int
	EventQueueSize int
	WindowSeconds  int
	MaxTrackedKeys int

	AlertIPThreshold  int64
	Alert5xxRate      float64
	AlertMinSamples   int64
	AlertCooldown     time.Duration
	AlertEvalInterval time.Duration
	AlertHistory      int
	MaskIPs           ipmask.Mode
	LogLevel          slog.Level
	ShutdownTimeout   time.Duration
}

// Var documenta uma variável de ambiente (usada no README e em -help).
type Var struct{ Name, Default, Help string }

// Vars lista todas as variáveis suportadas.
var Vars = []Var{
	{"OBSERVER_LISTEN_ADDR", ":8080", "endereço HTTP da API/dashboard"},
	{"OBSERVER_LOG_PATH", "/logs/access.jsonl", "arquivo JSON Lines gerado pelo NGINX"},
	{"OBSERVER_READ_FROM_START", "false", "processar conteúdo já existente no arquivo"},
	{"OBSERVER_POLL_INTERVAL", "250ms", "intervalo de verificação de novas linhas"},
	{"OBSERVER_MAX_LINE_BYTES", "16384", "linhas maiores são descartadas"},
	{"OBSERVER_WORKERS", "4", "goroutines de parsing (1..64)"},
	{"OBSERVER_RAW_QUEUE", "1024", "capacidade do channel de linhas brutas"},
	{"OBSERVER_EVENT_QUEUE", "1024", "capacidade do channel de eventos"},
	{"OBSERVER_WINDOW_SECONDS", "60", "tamanho da janela móvel"},
	{"OBSERVER_MAX_TRACKED_KEYS", "1000", "limite de endpoints/IPs distintos em memória"},
	{"OBSERVER_ALERT_IP_THRESHOLD", "300", "regra A: requisições por IP na janela (0 desativa)"},
	{"OBSERVER_ALERT_5XX_RATE", "0.20", "regra B: proporção de 5xx na janela (0 desativa)"},
	{"OBSERVER_ALERT_MIN_SAMPLES", "50", "regra B: mínimo de requisições na janela"},
	{"OBSERVER_ALERT_COOLDOWN", "60s", "intervalo mínimo entre alertas iguais"},
	{"OBSERVER_ALERT_INTERVAL", "1s", "frequência de avaliação das regras"},
	{"OBSERVER_ALERT_HISTORY", "100", "alertas mantidos em memória"},
	{"OBSERVER_MASK_IPS", "partial", "none | partial (/24 IPv4, /48 IPv6)"},
	{"OBSERVER_LOG_LEVEL", "info", "debug | info | warn | error"},
	{"OBSERVER_SHUTDOWN_TIMEOUT", "5s", "tempo máximo do desligamento gracioso"},
}

// FromEnv lê a configuração usando lookup (os.LookupEnv em produção).
func FromEnv(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	get := func(name string) string {
		if v, ok := lookup(name); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		for _, v := range Vars {
			if v.Name == name {
				return v.Default
			}
		}
		return ""
	}

	var errs []error
	intVar := func(name string, lo, hi int) int {
		n, err := strconv.Atoi(get(name))
		if err != nil || n < lo || n > hi {
			errs = append(errs, fmt.Errorf("%s: esperado inteiro entre %d e %d", name, lo, hi))
		}
		return n
	}
	durVar := func(name string, lo time.Duration) time.Duration {
		d, err := time.ParseDuration(get(name))
		if err != nil || d < lo {
			errs = append(errs, fmt.Errorf("%s: duração inválida (mínimo %s)", name, lo))
		}
		return d
	}
	boolVar := func(name string) bool {
		b, err := strconv.ParseBool(get(name))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: esperado true/false", name))
		}
		return b
	}

	c := Config{
		ListenAddr:        get("OBSERVER_LISTEN_ADDR"),
		LogPath:           get("OBSERVER_LOG_PATH"),
		ReadFromStart:     boolVar("OBSERVER_READ_FROM_START"),
		PollInterval:      durVar("OBSERVER_POLL_INTERVAL", 10*time.Millisecond),
		MaxLineBytes:      intVar("OBSERVER_MAX_LINE_BYTES", 256, 1<<20),
		Workers:           intVar("OBSERVER_WORKERS", 1, 64),
		RawQueueSize:      intVar("OBSERVER_RAW_QUEUE", 1, 1<<20),
		EventQueueSize:    intVar("OBSERVER_EVENT_QUEUE", 1, 1<<20),
		WindowSeconds:     intVar("OBSERVER_WINDOW_SECONDS", 5, 3600),
		MaxTrackedKeys:    intVar("OBSERVER_MAX_TRACKED_KEYS", 10, 1_000_000),
		AlertIPThreshold:  int64(intVar("OBSERVER_ALERT_IP_THRESHOLD", 0, 1<<30)),
		AlertMinSamples:   int64(intVar("OBSERVER_ALERT_MIN_SAMPLES", 1, 1<<30)),
		AlertCooldown:     durVar("OBSERVER_ALERT_COOLDOWN", 0),
		AlertEvalInterval: durVar("OBSERVER_ALERT_INTERVAL", 100*time.Millisecond),
		AlertHistory:      intVar("OBSERVER_ALERT_HISTORY", 1, 10_000),
		ShutdownTimeout:   durVar("OBSERVER_SHUTDOWN_TIMEOUT", 100*time.Millisecond),
	}
	if c.LogPath == "" {
		errs = append(errs, errors.New("OBSERVER_LOG_PATH vazio"))
	}

	rate, err := strconv.ParseFloat(get("OBSERVER_ALERT_5XX_RATE"), 64)
	if err != nil || rate < 0 || rate > 1 {
		errs = append(errs, errors.New("OBSERVER_ALERT_5XX_RATE: esperado número entre 0 e 1"))
	}
	c.Alert5xxRate = rate

	if c.MaskIPs, err = ipmask.ParseMode(get("OBSERVER_MASK_IPS")); err != nil {
		errs = append(errs, fmt.Errorf("OBSERVER_MASK_IPS: %w", err))
	}
	if err := c.LogLevel.UnmarshalText([]byte(get("OBSERVER_LOG_LEVEL"))); err != nil {
		errs = append(errs, fmt.Errorf("OBSERVER_LOG_LEVEL: %w", err))
	}
	return c, errors.Join(errs...)
}
