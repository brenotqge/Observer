// Package api expõe as métricas via HTTP/JSON e serve o dashboard estático
// (embutido no binário com go:embed).
package api

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"gonginx-observer/internal/alerts"
	"gonginx-observer/internal/ipmask"
	"gonginx-observer/internal/metrics"
	"gonginx-observer/internal/pipeline"
	"gonginx-observer/internal/tailer"
)

//go:embed web
var webFS embed.FS

// Deps são os componentes consultados pela API.
type Deps struct {
	Store    *metrics.Store
	Alerts   *alerts.Engine
	Pipeline *pipeline.Pipeline
	Tailer   *tailer.Tailer
	Mask     ipmask.Mode
	Version  string
	Logger   *slog.Logger
}

// Server implementa os handlers.
type Server struct {
	d     Deps
	start time.Time
}

// New cria o servidor da API.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return &Server{d: d, start: time.Now()}
}

// Handler retorna o roteador HTTP (padrões de rota do Go 1.22+).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/v1/metrics", s.metrics)
	mux.HandleFunc("GET /api/v1/status-codes", s.statusCodes)
	mux.HandleFunc("GET /api/v1/top-endpoints", s.topEndpoints)
	mux.HandleFunc("GET /api/v1/top-ips", s.topIPs)
	mux.HandleFunc("GET /api/v1/alerts", s.alerts)
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint não encontrado"})
	})

	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServerFS(static))
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// limitParam lê ?limit= com valor padrão e teto.
func limitParam(r *http.Request, def, max int) (int, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > max {
		return 0, false
	}
	return n, true
}

func badLimit(w http.ResponseWriter, max int) {
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error": "parâmetro limit deve ser um inteiro entre 1 e " + strconv.Itoa(max),
	})
}

func now() time.Time { return time.Now().UTC() }

// ---------------------------------------------------------------------------

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	present := s.d.Tailer.Stats.FilePresent.Load()
	status := "ok"
	if !present {
		status = "degraded" // processo saudável, mas o log ainda não existe
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           status,
		"log_file_present": present,
		"uptime_seconds":   time.Since(s.start).Seconds(),
		"version":          s.d.Version,
	})
}

type queue struct {
	Len int `json:"len"`
	Cap int `json:"cap"`
}

type pipelineInfo struct {
	Workers        int   `json:"workers"`
	FilePresent    bool  `json:"log_file_present"`
	LinesRead      int64 `json:"lines_read"`
	BytesRead      int64 `json:"bytes_read"`
	Parsed         int64 `json:"parsed"`
	ParseErrors    int64 `json:"parse_errors"`
	OversizedLines int64 `json:"oversized_lines"`
	Aggregated     int64 `json:"aggregated"`
	Truncations    int64 `json:"truncations"`
	Rotations      int64 `json:"rotations"`
	RawQueue       queue `json:"raw_queue"`
	EventQueue     queue `json:"event_queue"`
}

type runtimeInfo struct {
	GoVersion      string `json:"go_version"`
	Goroutines     int    `json:"goroutines"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	NumGC          uint32 `json:"num_gc"`
}

func (s *Server) pipelineInfo() pipelineInfo {
	ts, ps := &s.d.Tailer.Stats, &s.d.Pipeline.Stats
	rl, rc, el, ec := s.d.Pipeline.QueueStats()
	return pipelineInfo{
		Workers:        s.d.Pipeline.Workers(),
		FilePresent:    ts.FilePresent.Load(),
		LinesRead:      ts.LinesRead.Load(),
		BytesRead:      ts.BytesRead.Load(),
		Parsed:         ps.Parsed.Load(),
		ParseErrors:    ps.ParseErrors.Load(),
		OversizedLines: ts.OversizedLines.Load(),
		Aggregated:     ps.Aggregated.Load(),
		Truncations:    ts.Truncations.Load(),
		Rotations:      ts.Rotations.Load(),
		RawQueue:       queue{rl, rc},
		EventQueue:     queue{el, ec},
	}
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	writeJSON(w, http.StatusOK, struct {
		GeneratedAt time.Time `json:"generated_at"`
		metrics.Snapshot
		Pipeline pipelineInfo `json:"pipeline"`
		Runtime  runtimeInfo  `json:"runtime"`
	}{
		GeneratedAt: now(),
		Snapshot:    s.d.Store.Snapshot(),
		Pipeline:    s.pipelineInfo(),
		Runtime: runtimeInfo{
			GoVersion:      runtime.Version(),
			Goroutines:     runtime.NumGoroutine(),
			HeapAllocBytes: ms.HeapAlloc,
			SysBytes:       ms.Sys,
			NumGC:          ms.NumGC,
		},
	})
}

func (s *Server) statusCodes(w http.ResponseWriter, _ *http.Request) {
	codes, classes := s.d.Store.StatusCodes()
	win := s.d.Store.WindowSummary()
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": now(),
		"classes":      classes,
		"codes":        codes,
		"window": map[string]any{
			"seconds":    win.Seconds,
			"requests":   win.Requests,
			"errors_4xx": win.Errors4xx,
			"errors_5xx": win.Errors5xx,
		},
	})
}

func (s *Server) topEndpoints(w http.ResponseWriter, r *http.Request) {
	n, ok := limitParam(r, 10, 100)
	if !ok {
		badLimit(w, 100)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": now(),
		"limit":        n,
		"items":        s.d.Store.TopEndpoints(n),
	})
}

func (s *Server) topIPs(w http.ResponseWriter, r *http.Request) {
	n, ok := limitParam(r, 10, 100)
	if !ok {
		badLimit(w, 100)
		return
	}
	// Mascaramento antes do ranking: IPs do mesmo prefixo são somados.
	merged := make(map[string]int64)
	for ip, c := range s.d.Store.IPCounts() {
		merged[s.d.Mask.Apply(ip)] += c
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": now(),
		"limit":        n,
		"mask":         s.d.Mask,
		"items":        metrics.TopN(merged, n),
	})
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	n, ok := limitParam(r, 50, 1000)
	if !ok {
		badLimit(w, 1000)
		return
	}
	items, total := s.d.Alerts.Recent(n)
	for i := range items {
		if items[i].Type == alerts.TypeIPRequestRate {
			items[i].Entity = s.d.Mask.Apply(items[i].Entity)
		}
	}
	cfg := s.d.Alerts.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": now(),
		"total":        total,
		"items":        items,
		"rules": map[string]any{
			"ip_request_threshold": cfg.IPRequestThreshold,
			"server_error_rate":    cfg.ServerErrorRate,
			"min_samples":          cfg.MinSamples,
			"cooldown_seconds":     cfg.Cooldown.Seconds(),
		},
		"note": "Alertas são indicadores heurísticos e podem conter falsos positivos.",
	})
}
