// Package logparse interpreta linhas de access log do NGINX no formato
// JSON Lines definido em nginx/conf.d/default.conf (log_format observer_json).
package logparse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strings"
	"time"
)

// MaxPathLen limita o tamanho do caminho armazenado (cardinalidade/memória).
const MaxPathLen = 256

// Event é uma requisição HTTP já validada.
type Event struct {
	Time        time.Time
	RemoteAddr  netip.Addr
	Method      string
	Path        string
	Status      int
	BytesSent   int64
	RequestTime time.Duration
}

// Erros de validação (permitem contagem por categoria e testes com errors.Is).
var (
	ErrEmptyLine     = errors.New("linha vazia")
	ErrInvalidJSON   = errors.New("JSON inválido")
	ErrMissingField  = errors.New("campo obrigatório ausente")
	ErrInvalidField  = errors.New("campo com valor inválido")
	ErrNotJSONObject = errors.New("linha não é um objeto JSON")
)

// rawEvent espelha o JSON do NGINX. Ponteiros permitem distinguir
// "campo ausente" de "valor zero".
type rawEvent struct {
	TS          *float64 `json:"ts"`
	RemoteAddr  *string  `json:"remote_addr"`
	Method      *string  `json:"method"`
	URI         *string  `json:"uri"`
	Status      *int     `json:"status"`
	BytesSent   *int64   `json:"bytes_sent"`
	RequestTime *float64 `json:"request_time"`
}

// Parse interpreta e valida uma linha. Nunca entra em pânico com entrada
// arbitrária; registros inválidos retornam erro e devem ser descartados.
func Parse(line []byte) (Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Event{}, ErrEmptyLine
	}
	if line[0] != '{' {
		return Event{}, ErrNotJSONObject
	}

	var r rawEvent
	if err := json.Unmarshal(line, &r); err != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	switch {
	case r.TS == nil:
		return Event{}, fmt.Errorf("%w: ts", ErrMissingField)
	case r.RemoteAddr == nil:
		return Event{}, fmt.Errorf("%w: remote_addr", ErrMissingField)
	case r.Method == nil:
		return Event{}, fmt.Errorf("%w: method", ErrMissingField)
	case r.URI == nil:
		return Event{}, fmt.Errorf("%w: uri", ErrMissingField)
	case r.Status == nil:
		return Event{}, fmt.Errorf("%w: status", ErrMissingField)
	case r.BytesSent == nil:
		return Event{}, fmt.Errorf("%w: bytes_sent", ErrMissingField)
	case r.RequestTime == nil:
		return Event{}, fmt.Errorf("%w: request_time", ErrMissingField)
	}

	ts := *r.TS
	if ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) || ts > 1e11 {
		return Event{}, fmt.Errorf("%w: ts=%v", ErrInvalidField, ts)
	}
	addr, err := netip.ParseAddr(*r.RemoteAddr)
	if err != nil {
		return Event{}, fmt.Errorf("%w: remote_addr", ErrInvalidField)
	}
	if *r.Status < 100 || *r.Status > 599 {
		return Event{}, fmt.Errorf("%w: status=%d", ErrInvalidField, *r.Status)
	}
	if *r.BytesSent < 0 {
		return Event{}, fmt.Errorf("%w: bytes_sent", ErrInvalidField)
	}
	rt := *r.RequestTime
	if rt < 0 || math.IsNaN(rt) || math.IsInf(rt, 0) || rt > 86400 {
		return Event{}, fmt.Errorf("%w: request_time", ErrInvalidField)
	}

	sec, frac := math.Modf(ts)
	return Event{
		Time:        time.Unix(int64(sec), int64(math.Round(frac*1e3))*int64(time.Millisecond)),
		RemoteAddr:  addr.Unmap(),
		Method:      normalizeMethod(*r.Method),
		Path:        normalizePath(*r.URI),
		Status:      *r.Status,
		BytesSent:   *r.BytesSent,
		RequestTime: time.Duration(math.Round(rt * float64(time.Second))),
	}, nil
}

// normalizeMethod aceita apenas tokens curtos em maiúsculas. Requisições
// malformadas (ex.: 400 sem request line) chegam com método vazio.
func normalizeMethod(m string) string {
	if m == "" || len(m) > 16 {
		return "UNKNOWN"
	}
	for i := 0; i < len(m); i++ {
		if c := m[i]; c < 'A' || c > 'Z' {
			return "UNKNOWN"
		}
	}
	return m
}

// normalizePath remove query string residual (privacidade), limita o
// tamanho e substitui valores vazios.
func normalizePath(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" || p[0] != '/' {
		return "(invalid)"
	}
	if len(p) > MaxPathLen {
		p = p[:MaxPathLen]
	}
	return p
}
