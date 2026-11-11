package alerts

import (
	"context"
	"testing"
	"time"

	"gonginx-observer/internal/metrics"
)

type fakeSource struct {
	ips map[string]int64
	win metrics.Window
}

func (f *fakeSource) WindowIPCounts() map[string]int64 { return f.ips }
func (f *fakeSource) WindowSummary() metrics.Window    { return f.win }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newEngine(src Source, clk *clock) *Engine {
	return New(src, Config{
		IPRequestThreshold: 100,
		ServerErrorRate:    0.2,
		MinSamples:         50,
		Cooldown:           time.Minute,
		MaxAlerts:          3,
		Now:                clk.now,
	})
}

func TestRuleIPRequestRate(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	src := &fakeSource{ips: map[string]int64{"192.0.2.1": 100, "192.0.2.2": 101}, win: metrics.Window{Seconds: 60}}
	e := newEngine(src, clk)

	fired := e.Evaluate()
	if len(fired) != 1 {
		t.Fatalf("esperava 1 alerta (limite é estrito: > 100), veio %d", len(fired))
	}
	a := fired[0]
	if a.Type != TypeIPRequestRate || a.Entity != "192.0.2.2" || a.Observed != 101 || a.Threshold != 100 ||
		a.WindowSeconds != 60 || !a.DetectedAt.Equal(clk.t) || a.Description == "" || a.ID != 1 {
		t.Errorf("alerta incorreto: %+v", a)
	}

	// Cooldown: mesma condição não gera novo alerta antes de 1 min.
	clk.t = clk.t.Add(30 * time.Second)
	if n := len(e.Evaluate()); n != 0 {
		t.Errorf("cooldown violado: %d alertas", n)
	}
	clk.t = clk.t.Add(31 * time.Second)
	if n := len(e.Evaluate()); n != 1 {
		t.Errorf("após cooldown esperava 1 alerta, veio %d", n)
	}
}

func TestRuleServerErrors(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	src := &fakeSource{win: metrics.Window{Seconds: 60, Requests: 10, Errors5xx: 9, ServerErrorRate: 0.9}}
	e := newEngine(src, clk)

	if n := len(e.Evaluate()); n != 0 {
		t.Fatalf("abaixo do mínimo de amostras não deve alertar (veio %d)", n)
	}
	src.win = metrics.Window{Seconds: 60, Requests: 100, Errors5xx: 20, ServerErrorRate: 0.2}
	if n := len(e.Evaluate()); n != 0 {
		t.Fatalf("taxa igual ao limite não deve alertar (veio %d)", n)
	}
	src.win = metrics.Window{Seconds: 60, Requests: 100, Errors5xx: 35, ServerErrorRate: 0.35}
	fired := e.Evaluate()
	if len(fired) != 1 || fired[0].Type != TypeServerErrors || fired[0].Observed != 0.35 || fired[0].Entity != "global" {
		t.Fatalf("alerta 5xx incorreto: %+v", fired)
	}
}

func TestDisabledRules(t *testing.T) {
	src := &fakeSource{ips: map[string]int64{"x": 1e9}, win: metrics.Window{Requests: 1e6, ServerErrorRate: 1}}
	e := New(src, Config{}) // limites zero = regras desativadas
	if n := len(e.Evaluate()); n != 0 {
		t.Errorf("regras desativadas geraram %d alertas", n)
	}
}

func TestRecentIsBoundedAndNewestFirst(t *testing.T) {
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	src := &fakeSource{ips: map[string]int64{}, win: metrics.Window{Seconds: 60}}
	e := newEngine(src, clk)
	for _, ip := range []string{"a", "b", "c", "d", "e"} {
		src.ips = map[string]int64{ip: 500}
		e.Evaluate()
	}
	got, total := e.Recent(10)
	if total != 5 || len(got) != 3 {
		t.Fatalf("total=%d len=%d (MaxAlerts=3)", total, len(got))
	}
	if got[0].Entity != "e" || got[2].Entity != "c" {
		t.Errorf("ordem incorreta: %v %v %v", got[0].Entity, got[1].Entity, got[2].Entity)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	e := New(&fakeSource{}, Config{Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run não encerrou")
	}
}

// Integração com o Store real: 5xx em janela de 60 s.
func TestWithRealStore(t *testing.T) {
	s := metrics.New(metrics.Config{})
	e := New(s, Config{ServerErrorRate: 0.5, MinSamples: 1})
	if n := len(e.Evaluate()); n != 0 {
		t.Fatalf("store vazio gerou %d alertas", n)
	}
}
