package metrics

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"gonginx-observer/internal/logparse"
)

// fakeClock é um relógio controlado pelo teste.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newClock() *fakeClock { return &fakeClock{t: time.Unix(1_800_000_000, 0)} }

func ev(t time.Time, ip string, path string, status int, bytes int64, dur time.Duration) logparse.Event {
	return logparse.Event{
		Time: t, RemoteAddr: netip.MustParseAddr(ip), Method: "GET", Path: path,
		Status: status, BytesSent: bytes, RequestTime: dur,
	}
}

func TestEmptySnapshot(t *testing.T) {
	s := New(Config{Now: newClock().Now})
	snap := s.Snapshot()
	if snap.Cumulative.TotalRequests != 0 || snap.Window.RequestsPerSec != 0 || snap.LastEventAt != nil {
		t.Errorf("snapshot vazio incorreto: %+v", snap)
	}
	if len(snap.History) != 60 {
		t.Errorf("histórico deve ter 60 pontos, tem %d", len(snap.History))
	}
	if len(s.TopEndpoints(10)) != 0 {
		t.Error("top endpoints deveria estar vazio")
	}
}

func TestAggregation(t *testing.T) {
	clk := newClock()
	s := New(Config{Now: clk.Now})
	now := clk.Now()
	s.Add(ev(now, "192.0.2.1", "/", 200, 100, 10*time.Millisecond))
	s.Add(ev(now, "192.0.2.1", "/", 200, 100, 30*time.Millisecond))
	s.Add(ev(now, "192.0.2.2", "/missing", 404, 50, 0))
	s.Add(ev(now, "192.0.2.3", "/error", 500, 50, 0))
	s.Add(ev(now, "192.0.2.3", "/redir", 301, 0, 0))

	c := s.Snapshot().Cumulative
	if c.TotalRequests != 5 || c.BytesSent != 300 {
		t.Errorf("totais: %+v", c)
	}
	if c.StatusClasses != (Classes{C2xx: 2, C3xx: 1, C4xx: 1, C5xx: 1}) {
		t.Errorf("classes: %+v", c.StatusClasses)
	}
	if c.ErrorRate != 0.4 || c.ServerErrorRate != 0.2 {
		t.Errorf("taxas: %v %v", c.ErrorRate, c.ServerErrorRate)
	}
	if c.AvgResponseMs != 8 {
		t.Errorf("média = %v ms, quer 8", c.AvgResponseMs)
	}
	codes, _ := s.StatusCodes()
	if len(codes) != 4 || codes[0].Code != 200 || codes[0].Count != 2 {
		t.Errorf("códigos: %+v", codes)
	}
	top := s.TopEndpoints(1)
	if len(top) != 1 || top[0] != (Counter{"GET /", 2}) {
		t.Errorf("top endpoints: %+v", top)
	}
	if ips := TopN(s.IPCounts(), 2); ips[0] != (Counter{"192.0.2.1", 2}) || ips[1] != (Counter{"192.0.2.3", 2}) {
		t.Errorf("top IPs (desempate por chave): %+v", ips)
	}
}

func TestSlidingWindow(t *testing.T) {
	clk := newClock()
	s := New(Config{Now: clk.Now, WindowSeconds: 60})
	clk.Advance(120 * time.Second) // processo "antigo": taxa usa janela completa

	for i := range 30 {
		s.Add(ev(clk.Now(), "192.0.2.1", "/", 200, 1, 0))
		if i%10 == 9 {
			clk.Advance(10 * time.Second)
		}
	}
	// t0 = início; eventos em t0, t0+10, t0+20; agora = t0+30.
	w := s.WindowSummary()
	if w.Requests != 30 {
		t.Fatalf("janela = %d, quer 30", w.Requests)
	}
	if got := s.Snapshot().Window.RequestsPerSec; got != 0.5 {
		t.Errorf("rps = %v, quer 0.5 (30/60)", got)
	}

	clk.Advance(35 * time.Second) // agora = t0+65: bloco de t0 expirou
	if w := s.WindowSummary(); w.Requests != 20 {
		t.Errorf("após expirar 1º bloco: %d, quer 20", w.Requests)
	}
	if n := s.WindowIPCounts()["192.0.2.1"]; n != 20 {
		t.Errorf("contagem por IP na janela = %d, quer 20", n)
	}
	clk.Advance(60 * time.Second)
	if w := s.WindowSummary(); w.Requests != 0 || w.DistinctIPs != 0 {
		t.Errorf("janela deveria estar vazia: %+v", w)
	}
	if len(s.WindowIPCounts()) != 0 {
		t.Error("mapa de IPs da janela deveria ser liberado")
	}
	// Acumulado não expira.
	if s.Snapshot().Cumulative.TotalRequests != 30 {
		t.Error("acumulado não pode expirar")
	}
}

func TestLateAndFutureEvents(t *testing.T) {
	clk := newClock()
	s := New(Config{Now: clk.Now})
	s.Add(ev(clk.Now().Add(-5*time.Minute), "192.0.2.1", "/", 200, 1, 0)) // atrasado
	s.Add(ev(clk.Now().Add(time.Hour), "192.0.2.1", "/", 200, 1, 0))      // relógio adiantado
	snap := s.Snapshot()
	if snap.Cumulative.TotalRequests != 2 || snap.Cumulative.LateEvents != 1 || snap.Window.Requests != 1 {
		t.Errorf("snapshot: %+v", snap)
	}
	if last := snap.History[len(snap.History)-1]; last.Requests != 1 {
		t.Errorf("evento futuro deveria cair no segundo atual: %+v", last)
	}
}

func TestEarlyRateUsesUptime(t *testing.T) {
	clk := newClock()
	s := New(Config{Now: clk.Now})
	clk.Advance(10 * time.Second)
	for range 20 {
		s.Add(ev(clk.Now(), "192.0.2.1", "/", 200, 1, 0))
	}
	if got := s.Snapshot().Window.RequestsPerSec; got != 2 {
		t.Errorf("rps nos primeiros 10 s = %v, quer 2", got)
	}
}

func TestCardinalityLimit(t *testing.T) {
	clk := newClock()
	s := New(Config{Now: clk.Now, MaxKeys: 10})
	for i := range 50 {
		s.Add(ev(clk.Now(), fmt.Sprintf("10.0.0.%d", i), fmt.Sprintf("/p/%d", i), 200, 1, 0))
	}
	if n := len(s.IPCounts()); n != 11 { // 10 + "(other)"
		t.Errorf("IPs distintos = %d, quer 11", n)
	}
	if got := s.IPCounts()[OtherKey]; got != 40 {
		t.Errorf("(other) = %d, quer 40", got)
	}
	if n := len(s.WindowIPCounts()); n != 10 {
		t.Errorf("IPs na janela = %d, quer 10", n)
	}
	if s.Snapshot().Cumulative.TotalRequests != 50 {
		t.Error("nenhuma requisição pode ser perdida pelo limite")
	}
}

func TestHistoryOrder(t *testing.T) {
	clk := newClock()
	s := New(Config{Now: clk.Now})
	s.Add(ev(clk.Now(), "192.0.2.1", "/", 500, 1, 0))
	clk.Advance(2 * time.Second)
	s.Add(ev(clk.Now(), "192.0.2.1", "/", 404, 1, 0))
	h := s.Snapshot().History
	last := h[len(h)-1]
	twoAgo := h[len(h)-3]
	if last.Time != clk.Now().Unix() || last.Errors4xx != 1 || twoAgo.Errors5xx != 1 {
		t.Errorf("histórico: último=%+v, -2s=%+v", last, twoAgo)
	}
}

// Leitores concorrentes com um escritor: deve passar sob -race.
func TestConcurrentReadersAndWriter(t *testing.T) {
	s := New(Config{})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Snapshot()
					_ = s.TopEndpoints(5)
					_ = s.WindowIPCounts()
				}
			}
		})
	}
	for i := range 5000 {
		s.Add(ev(time.Now(), fmt.Sprintf("10.0.%d.%d", i%7, i%13), "/", 200+(i%4)*100, 10, time.Millisecond))
	}
	close(stop)
	wg.Wait()
	if got := s.Snapshot().Cumulative.TotalRequests; got != 5000 {
		t.Errorf("total = %d", got)
	}
}

func BenchmarkAdd(b *testing.B) {
	s := New(Config{})
	e := ev(time.Now(), "192.0.2.1", "/api/items", 200, 213, time.Millisecond)
	b.ReportAllocs()
	for b.Loop() {
		s.Add(e)
	}
}
