// Package metrics mantém as métricas agregadas em memória.
//
// Modelo de concorrência: um único escritor (a goroutine agregadora do
// pipeline chama Add) e vários leitores (handlers HTTP, motor de alertas).
// O estado é protegido por sync.RWMutex; leitores recebem cópias, nunca
// referências ao estado interno.
//
// Dois tipos de métrica:
//   - Acumuladas: desde a inicialização do processo (zeradas ao reiniciar).
//   - Em janela: últimos WindowSeconds segundos, em buckets de 1 s
//     (ring buffer), indexados pelo timestamp do evento.
package metrics

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"gonginx-observer/internal/logparse"
)

// OtherKey agrega chaves que excedem o limite de cardinalidade.
const OtherKey = "(other)"

// Config parametriza o Store.
type Config struct {
	WindowSeconds int // padrão 60
	MaxKeys       int // limite de chaves distintas por mapa (endpoints, IPs)
	Now           func() time.Time
}

type bucket struct {
	sec      int64
	requests int64
	c4xx     int64
	c5xx     int64
	bytes    int64
	dur      time.Duration
	ips      map[string]int64
	overflow int64 // requisições de IPs fora do limite de cardinalidade
}

// Store guarda as métricas.
type Store struct {
	mu      sync.RWMutex
	now     func() time.Time
	window  int64
	maxKeys int
	start   time.Time

	// Acumuladas.
	total     int64
	bytes     int64
	durSum    time.Duration
	classes   [6]int64 // índice = status/100 (1..5)
	byCode    map[int]int64
	endpoints map[string]int64
	ips       map[string]int64
	lastEvent time.Time
	late      int64 // eventos antigos demais para a janela

	// Janela móvel.
	buckets     []bucket
	winIPs      map[string]int64
	advancedSec int64
}

// New cria um Store vazio.
func New(cfg Config) *Store {
	if cfg.WindowSeconds <= 0 {
		cfg.WindowSeconds = 60
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = 1000
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Store{
		now:       cfg.Now,
		window:    int64(cfg.WindowSeconds),
		maxKeys:   cfg.MaxKeys,
		start:     cfg.Now(),
		byCode:    make(map[int]int64),
		endpoints: make(map[string]int64),
		ips:       make(map[string]int64),
		buckets:   make([]bucket, cfg.WindowSeconds),
		winIPs:    make(map[string]int64),
	}
}

// boundedInc incrementa m[key] respeitando o limite de chaves distintas.
func boundedInc(m map[string]int64, key string, max int) {
	if _, ok := m[key]; !ok && len(m) >= max {
		key = OtherKey
	}
	m[key]++
}

// Add contabiliza um evento. Deve ser chamado por um único escritor, mas é
// seguro em qualquer caso graças ao mutex.
func (s *Store) Add(ev logparse.Event) {
	ip := ev.RemoteAddr.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.total++
	s.bytes += ev.BytesSent
	s.durSum += ev.RequestTime
	s.classes[ev.Status/100]++
	s.byCode[ev.Status]++
	boundedInc(s.endpoints, ev.Method+" "+ev.Path, s.maxKeys)
	boundedInc(s.ips, ip, s.maxKeys)
	if ev.Time.After(s.lastEvent) {
		s.lastEvent = ev.Time
	}

	nowSec := s.now().Unix()
	s.advance(nowSec)
	sec := ev.Time.Unix()
	if sec > nowSec { // relógio adiantado: atribui ao segundo atual
		sec = nowSec
	}
	if sec <= nowSec-s.window {
		s.late++
		return
	}
	b := &s.buckets[sec%s.window]
	if b.sec != sec {
		s.resetBucket(b, sec)
	}
	b.requests++
	switch ev.Status / 100 {
	case 4:
		b.c4xx++
	case 5:
		b.c5xx++
	}
	b.bytes += ev.BytesSent
	b.dur += ev.RequestTime
	if _, tracked := s.winIPs[ip]; tracked || len(s.winIPs) < s.maxKeys {
		if b.ips == nil {
			b.ips = make(map[string]int64)
		}
		b.ips[ip]++
		s.winIPs[ip]++
	} else {
		b.overflow++
	}
}

// advance expira buckets que saíram da janela. Executa no máximo uma vez
// por segundo de relógio.
func (s *Store) advance(nowSec int64) {
	if nowSec == s.advancedSec {
		return
	}
	s.advancedSec = nowSec
	for i := range s.buckets {
		b := &s.buckets[i]
		if b.requests > 0 && b.sec <= nowSec-s.window {
			s.resetBucket(b, 0)
		}
	}
}

func (s *Store) resetBucket(b *bucket, sec int64) {
	for ip, n := range b.ips {
		if left := s.winIPs[ip] - n; left > 0 {
			s.winIPs[ip] = left
		} else {
			delete(s.winIPs, ip)
		}
	}
	clear(b.ips)
	*b = bucket{sec: sec, ips: b.ips}
}

// ---------------------------------------------------------------------------
// Leitura (cópias imutáveis).
// ---------------------------------------------------------------------------

// Classes conta respostas por classe de status.
type Classes struct {
	C1xx int64 `json:"1xx"`
	C2xx int64 `json:"2xx"`
	C3xx int64 `json:"3xx"`
	C4xx int64 `json:"4xx"`
	C5xx int64 `json:"5xx"`
}

// Cumulative são métricas desde a inicialização.
type Cumulative struct {
	TotalRequests   int64   `json:"total_requests"`
	StatusClasses   Classes `json:"status_classes"`
	ErrorRate       float64 `json:"error_rate"`        // (4xx+5xx)/total
	ServerErrorRate float64 `json:"server_error_rate"` // 5xx/total
	AvgResponseMs   float64 `json:"avg_response_ms"`
	BytesSent       int64   `json:"bytes_sent"`
	LateEvents      int64   `json:"late_events"`
}

// Window são métricas da janela móvel.
type Window struct {
	Seconds         int64   `json:"seconds"`
	Requests        int64   `json:"requests"`
	RequestsPerSec  float64 `json:"requests_per_second"`
	Errors4xx       int64   `json:"errors_4xx"`
	Errors5xx       int64   `json:"errors_5xx"`
	ErrorRate       float64 `json:"error_rate"`
	ServerErrorRate float64 `json:"server_error_rate"`
	AvgResponseMs   float64 `json:"avg_response_ms"`
	BytesSent       int64   `json:"bytes_sent"`
	DistinctIPs     int     `json:"distinct_ips"`
}

// HistoryPoint é um segundo do histórico curto.
type HistoryPoint struct {
	Time      int64 `json:"t"` // unix seconds
	Requests  int64 `json:"requests"`
	Errors4xx int64 `json:"errors_4xx"`
	Errors5xx int64 `json:"errors_5xx"`
}

// Snapshot é a visão consolidada para a API.
type Snapshot struct {
	UptimeSeconds float64        `json:"uptime_seconds"`
	LastEventAt   *time.Time     `json:"last_event_at"`
	Cumulative    Cumulative     `json:"cumulative"`
	Window        Window         `json:"window"`
	History       []HistoryPoint `json:"history"`
}

// Counter é um par chave/contagem para rankings.
type Counter struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

func ratio(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func avgMs(d time.Duration, n int64) float64 {
	if n == 0 {
		return 0
	}
	return float64(d) / float64(n) / float64(time.Millisecond)
}

// Snapshot retorna todas as métricas principais.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock() // advance pode modificar a janela
	defer s.mu.Unlock()

	now := s.now()
	nowSec := now.Unix()
	s.advance(nowSec)

	uptime := now.Sub(s.start).Seconds()
	snap := Snapshot{
		UptimeSeconds: uptime,
		Cumulative: Cumulative{
			TotalRequests: s.total,
			StatusClasses: Classes{
				C1xx: s.classes[1], C2xx: s.classes[2], C3xx: s.classes[3],
				C4xx: s.classes[4], C5xx: s.classes[5],
			},
			ErrorRate:       ratio(s.classes[4]+s.classes[5], s.total),
			ServerErrorRate: ratio(s.classes[5], s.total),
			AvgResponseMs:   avgMs(s.durSum, s.total),
			BytesSent:       s.bytes,
			LateEvents:      s.late,
		},
	}
	if !s.lastEvent.IsZero() {
		t := s.lastEvent
		snap.LastEventAt = &t
	}

	w := s.windowLocked(nowSec)
	// Enquanto o processo tem menos de uma janela de vida, a taxa usa o
	// tempo efetivamente observado (mínimo de 1 s).
	elapsed := min(float64(s.window), max(uptime, 1))
	w.RequestsPerSec = float64(w.Requests) / elapsed
	snap.Window = w

	snap.History = make([]HistoryPoint, 0, s.window)
	for sec := nowSec - s.window + 1; sec <= nowSec; sec++ {
		p := HistoryPoint{Time: sec}
		if b := &s.buckets[sec%s.window]; b.sec == sec {
			p.Requests, p.Errors4xx, p.Errors5xx = b.requests, b.c4xx, b.c5xx
		}
		snap.History = append(snap.History, p)
	}
	return snap
}

func (s *Store) windowLocked(nowSec int64) Window {
	w := Window{Seconds: s.window}
	var dur time.Duration
	for i := range s.buckets {
		b := &s.buckets[i]
		if b.sec <= nowSec-s.window || b.sec > nowSec {
			continue
		}
		w.Requests += b.requests
		w.Errors4xx += b.c4xx
		w.Errors5xx += b.c5xx
		w.BytesSent += b.bytes
		dur += b.dur
	}
	w.ErrorRate = ratio(w.Errors4xx+w.Errors5xx, w.Requests)
	w.ServerErrorRate = ratio(w.Errors5xx, w.Requests)
	w.AvgResponseMs = avgMs(dur, w.Requests)
	w.DistinctIPs = len(s.winIPs)
	return w
}

// WindowSummary retorna apenas as métricas da janela (usado pelos alertas).
func (s *Store) WindowSummary() Window {
	s.mu.Lock()
	defer s.mu.Unlock()
	nowSec := s.now().Unix()
	s.advance(nowSec)
	return s.windowLocked(nowSec)
}

// WindowIPCounts retorna uma cópia das contagens por IP na janela.
func (s *Store) WindowIPCounts() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance(s.now().Unix())
	out := make(map[string]int64, len(s.winIPs))
	for k, v := range s.winIPs {
		out[k] = v
	}
	return out
}

// StatusCodes retorna contagens acumuladas por código HTTP, ordenadas.
func (s *Store) StatusCodes() (byCode []CodeCount, classes Classes) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byCode = make([]CodeCount, 0, len(s.byCode))
	for c, n := range s.byCode {
		byCode = append(byCode, CodeCount{Code: c, Count: n})
	}
	slices.SortFunc(byCode, func(a, b CodeCount) int { return cmp.Compare(a.Code, b.Code) })
	classes = Classes{C1xx: s.classes[1], C2xx: s.classes[2], C3xx: s.classes[3], C4xx: s.classes[4], C5xx: s.classes[5]}
	return byCode, classes
}

// CodeCount é a contagem de um código HTTP.
type CodeCount struct {
	Code  int   `json:"code"`
	Count int64 `json:"count"`
}

// TopEndpoints retorna os n endpoints ("MÉTODO /caminho") mais acessados.
func (s *Store) TopEndpoints(n int) []Counter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return TopN(s.endpoints, n)
}

// IPCounts retorna uma cópia das contagens acumuladas por IP.
func (s *Store) IPCounts() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int64, len(s.ips))
	for k, v := range s.ips {
		out[k] = v
	}
	return out
}

// TopN ordena por contagem decrescente (desempate pela chave) e corta em n.
func TopN(m map[string]int64, n int) []Counter {
	out := make([]Counter, 0, len(m))
	for k, v := range m {
		out = append(out, Counter{Key: k, Count: v})
	}
	slices.SortFunc(out, func(a, b Counter) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	if n >= 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
