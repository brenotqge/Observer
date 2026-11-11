package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gonginx-observer/internal/config"
)

func testConfig(t *testing.T, logPath string) config.Config {
	t.Helper()
	cfg, err := config.FromEnv(func(k string) (string, bool) {
		v, ok := map[string]string{
			"OBSERVER_LISTEN_ADDR":        "127.0.0.1:0",
			"OBSERVER_LOG_PATH":           logPath,
			"OBSERVER_POLL_INTERVAL":      "10ms",
			"OBSERVER_ALERT_INTERVAL":     "100ms",
			"OBSERVER_ALERT_IP_THRESHOLD": "5",
			"OBSERVER_MASK_IPS":           "none",
		}[k]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Teste de ponta a ponta em processo: arquivo → pipeline → API → alertas →
// desligamento gracioso sem vazamento de goroutines.
func TestRunEndToEndAndGracefulShutdown(t *testing.T) {
	before := runtime.NumGoroutine()
	logPath := filepath.Join(t.TempDir(), "access.jsonl")
	if err := os.WriteFile(logPath, []byte(`{"ts":1,"remote_addr":"1.1.1.1","method":"GET","uri":"/antigo","status":200,"bytes_sent":1,"request_time":0}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, testConfig(t, logPath), "test", slog.New(slog.DiscardHandler), func(a string) { addrCh <- a })
	}()
	var base string
	select {
	case a := <-addrCh:
		base = "http://" + a
	case err := <-done:
		t.Fatalf("Run falhou: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("servidor não iniciou")
	}

	waitFor(t, func() bool { return get(t, base+"/healthz")["log_file_present"] == true })

	// Linhas novas (inclui uma inválida, que deve ser ignorada).
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := float64(time.Now().UnixMilli()) / 1000
	for i := range 10 {
		fmt.Fprintf(f, `{"ts":%.3f,"remote_addr":"203.0.113.9","method":"GET","uri":"/n","status":%d,"bytes_sent":5,"request_time":0.002}`+"\n",
			now, []int{200, 404}[i%2])
	}
	fmt.Fprintln(f, `isto não é JSON`)
	f.Close()

	waitFor(t, func() bool {
		m := get(t, base+"/api/v1/metrics")
		return m["cumulative"].(map[string]any)["total_requests"].(float64) == 10
	})
	m := get(t, base+"/api/v1/metrics")
	if pe := m["pipeline"].(map[string]any)["parse_errors"].(float64); pe != 1 {
		t.Errorf("parse_errors = %v, quer 1", pe)
	}
	// A linha que existia antes da inicialização não foi processada.
	if ep := get(t, base+"/api/v1/top-endpoints")["items"].([]any); len(ep) != 1 {
		t.Errorf("endpoints: %v", ep)
	}
	// 10 req do mesmo IP > limite 5 → alerta.
	waitFor(t, func() bool { return get(t, base+"/api/v1/alerts")["total"].(float64) >= 1 })

	// Desligamento gracioso.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run retornou erro no desligamento: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("desligamento não concluiu")
	}
	if _, err := http.Get(base + "/healthz"); err == nil {
		t.Error("servidor ainda aceita conexões após o desligamento")
	}
	http.DefaultClient.CloseIdleConnections()
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before+1 })
}

func TestRunFailsOnBusyPort(t *testing.T) {
	cfg := testConfig(t, filepath.Join(t.TempDir(), "x"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	go Run(ctx, cfg, "t", slog.New(slog.DiscardHandler), func(a string) { addrCh <- a })
	cfg.ListenAddr = <-addrCh
	if err := Run(context.Background(), cfg, "t", slog.New(slog.DiscardHandler), nil); err == nil {
		t.Fatal("esperava erro com porta ocupada")
	}
}

func get(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condição não satisfeita a tempo")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
