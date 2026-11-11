package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"gonginx-observer/internal/alerts"
	"gonginx-observer/internal/ipmask"
	"gonginx-observer/internal/logparse"
	"gonginx-observer/internal/metrics"
	"gonginx-observer/internal/pipeline"
	"gonginx-observer/internal/tailer"
)

func newTestServer(t *testing.T, mask ipmask.Mode) (*httptest.Server, *metrics.Store, *alerts.Engine) {
	t.Helper()
	store := metrics.New(metrics.Config{})
	tl := tailer.New(tailer.Config{Path: "/nao/existe"})
	pl := pipeline.New(tl, store, pipeline.Config{Workers: 2})
	eng := alerts.New(store, alerts.Config{IPRequestThreshold: 2, Cooldown: time.Minute})
	srv := httptest.NewServer(New(Deps{Store: store, Alerts: eng, Pipeline: pl, Tailer: tl, Mask: mask, Version: "test"}).Handler())
	t.Cleanup(srv.Close)
	return srv, store, eng
}

func getJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s: status %d, quer %d", url, resp.StatusCode, wantStatus)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s: Content-Type %q", url, ct)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("%s: JSON inválido: %v", url, err)
	}
	return m
}

// Todos os endpoints devem responder JSON válido sem nenhum evento.
func TestEndpointsWithNoData(t *testing.T) {
	srv, _, _ := newTestServer(t, ipmask.None)
	for _, path := range []string{"/healthz", "/api/v1/metrics", "/api/v1/status-codes",
		"/api/v1/top-endpoints", "/api/v1/top-ips", "/api/v1/alerts"} {
		t.Run(path, func(t *testing.T) {
			m := getJSON(t, srv.URL+path, http.StatusOK)
			if items, ok := m["items"]; ok {
				if arr, isArr := items.([]any); !isArr || len(arr) != 0 {
					t.Errorf("items deveria ser [] (não null): %v", items)
				}
			}
		})
	}
	h := getJSON(t, srv.URL+"/healthz", http.StatusOK)
	if h["status"] != "degraded" || h["log_file_present"] != false {
		t.Errorf("healthz sem arquivo: %v", h)
	}
	m := getJSON(t, srv.URL+"/api/v1/metrics", http.StatusOK)
	if hist := m["history"].([]any); len(hist) != 60 {
		t.Errorf("history com %d pontos", len(hist))
	}
	for _, k := range []string{"cumulative", "window", "pipeline", "runtime"} {
		if _, ok := m[k]; !ok {
			t.Errorf("campo %q ausente em /metrics", k)
		}
	}
}

func addEvents(store *metrics.Store) {
	now := time.Now()
	for i, ip := range []string{"192.0.2.10", "192.0.2.10", "192.0.2.10", "192.0.2.20", "198.51.100.1"} {
		status := 200
		if i == 4 {
			status = 500
		}
		store.Add(logparse.Event{Time: now, RemoteAddr: netip.MustParseAddr(ip), Method: "GET",
			Path: "/x", Status: status, BytesSent: 10, RequestTime: time.Millisecond})
	}
}

func TestEndpointsWithData(t *testing.T) {
	srv, store, eng := newTestServer(t, ipmask.None)
	addEvents(store)
	eng.Evaluate()

	m := getJSON(t, srv.URL+"/api/v1/metrics", http.StatusOK)
	if total := m["cumulative"].(map[string]any)["total_requests"].(float64); total != 5 {
		t.Errorf("total = %v", total)
	}
	sc := getJSON(t, srv.URL+"/api/v1/status-codes", http.StatusOK)
	if c := sc["classes"].(map[string]any); c["2xx"].(float64) != 4 || c["5xx"].(float64) != 1 {
		t.Errorf("classes: %v", c)
	}
	ips := getJSON(t, srv.URL+"/api/v1/top-ips?limit=1", http.StatusOK)
	items := ips["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["key"] != "192.0.2.10" {
		t.Errorf("top-ips: %v", items)
	}
	al := getJSON(t, srv.URL+"/api/v1/alerts", http.StatusOK)
	if al["total"].(float64) != 1 {
		t.Errorf("alerts: %v", al)
	}
	a := al["items"].([]any)[0].(map[string]any)
	for _, k := range []string{"type", "detected_at", "entity", "metric", "observed", "threshold", "description"} {
		if _, ok := a[k]; !ok {
			t.Errorf("alerta sem campo %q", k)
		}
	}
}

func TestIPMasking(t *testing.T) {
	srv, store, eng := newTestServer(t, ipmask.Partial)
	addEvents(store)
	eng.Evaluate()
	ips := getJSON(t, srv.URL+"/api/v1/top-ips", http.StatusOK)
	items := ips["items"].([]any)
	first := items[0].(map[string]any)
	// 192.0.2.10 (3) + 192.0.2.20 (1) somados no mesmo /24.
	if first["key"] != "192.0.2.0/24" || first["count"].(float64) != 4 {
		t.Errorf("mascaramento/agrupamento: %v", items)
	}
	body := getBody(t, srv.URL+"/api/v1/alerts")
	if strings.Contains(body, "192.0.2.10") {
		t.Error("IP completo vazou em /alerts com mascaramento ativo")
	}
}

func TestBadRequestsAndRouting(t *testing.T) {
	srv, _, _ := newTestServer(t, ipmask.None)
	getJSON(t, srv.URL+"/api/v1/top-ips?limit=0", http.StatusBadRequest)
	getJSON(t, srv.URL+"/api/v1/top-endpoints?limit=abc", http.StatusBadRequest)
	getJSON(t, srv.URL+"/api/v1/nao-existe", http.StatusNotFound)

	resp, err := http.Post(srv.URL+"/api/v1/metrics", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics: %d, quer 405", resp.StatusCode)
	}
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func TestDashboardServed(t *testing.T) {
	srv, _, _ := newTestServer(t, ipmask.None)
	for path, want := range map[string]string{"/": "GoNginx Observer", "/app.js": "api/v1/metrics", "/style.css": "--surface"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
		if body := getBody(t, srv.URL+path); !strings.Contains(body, want) {
			t.Errorf("%s não contém %q", path, want)
		}
		if resp.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s sem CSP", path)
		}
	}
}
