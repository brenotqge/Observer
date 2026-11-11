package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonginx-observer/internal/logparse"
)

// sliceSource envia linhas fixas e depois espera o cancelamento, como um
// tailer real faria.
type sliceSource struct {
	lines [][]byte
	sent  atomic.Int64
}

func (s *sliceSource) Run(ctx context.Context, out chan<- []byte) error {
	for _, l := range s.lines {
		select {
		case out <- l:
			s.sent.Add(1)
		case <-ctx.Done():
			return nil
		}
	}
	<-ctx.Done()
	return nil
}

// countingSink registra quantas vezes cada requisição (identificada pelo
// bytes_sent único) foi agregada.
type countingSink struct {
	mu   sync.Mutex
	seen map[int64]int
	slow time.Duration
}

func (c *countingSink) Add(ev logparse.Event) {
	if c.slow > 0 {
		time.Sleep(c.slow)
	}
	c.mu.Lock()
	c.seen[ev.BytesSent]++
	c.mu.Unlock()
}

func makeLines(n, invalidEvery int) [][]byte {
	lines := make([][]byte, 0, n)
	for i := range n {
		if invalidEvery > 0 && i%invalidEvery == 0 {
			lines = append(lines, []byte(`{"quebrado":`))
			continue
		}
		lines = append(lines, fmt.Appendf(nil,
			`{"ts":1800000000.5,"remote_addr":"192.0.2.%d","method":"GET","uri":"/p","status":200,"bytes_sent":%d,"request_time":0.001}`,
			i%250, i))
	}
	return lines
}

// Cada evento válido deve ser contabilizado exatamente uma vez, com vários
// workers e channels pequenos (força contenção). Rode com -race.
func TestEachEventCountedExactlyOnce(t *testing.T) {
	const n, invalidEvery = 20_000, 7
	src := &sliceSource{lines: makeLines(n, invalidEvery)}
	sink := &countingSink{seen: make(map[int64]int)}
	p := New(src, sink, Config{Workers: 8, RawQueueSize: 4, EventQueueSize: 4})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for p.Stats.Aggregated.Load()+p.Stats.ParseErrors.Load() < n {
		if time.Now().After(deadline) {
			t.Fatal("timeout processando eventos")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	invalid := int64((n + invalidEvery - 1) / invalidEvery)
	if got := p.Stats.ParseErrors.Load(); got != invalid {
		t.Errorf("ParseErrors = %d, quer %d", got, invalid)
	}
	if got := p.Stats.Aggregated.Load(); got != n-invalid {
		t.Errorf("Aggregated = %d, quer %d", got, n-invalid)
	}
	for id, c := range sink.seen {
		if c != 1 {
			t.Fatalf("evento %d contabilizado %d vezes", id, c)
		}
	}
	if int64(len(sink.seen)) != n-invalid {
		t.Errorf("eventos distintos = %d", len(sink.seen))
	}
}

// No desligamento, tudo o que o leitor já entregou é drenado (nada perdido,
// nada duplicado) e Run retorna sem deadlock mesmo com agregador lento.
func TestShutdownDrainsInFlight(t *testing.T) {
	const n = 500
	src := &sliceSource{lines: makeLines(n, 0)}
	sink := &countingSink{seen: make(map[int64]int), slow: 50 * time.Microsecond}
	p := New(src, sink, Config{Workers: 4, RawQueueSize: 64, EventQueueSize: 64})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel() // cancela no meio do processamento

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock no desligamento")
	}
	if got, sent := p.Stats.Aggregated.Load(), src.sent.Load(); got != sent {
		t.Errorf("agregados = %d, enviados pelo leitor = %d (devem ser iguais)", got, sent)
	}
	for id, c := range sink.seen {
		if c != 1 {
			t.Fatalf("evento %d contabilizado %d vezes", id, c)
		}
	}
}

// Sobrecarga: com o agregador lento, as filas enchem até a capacidade e o
// leitor é bloqueado (backpressure) — sem crescimento ilimitado.
func TestBackpressureBoundsQueues(t *testing.T) {
	src := &sliceSource{lines: makeLines(2000, 0)}
	sink := &countingSink{seen: make(map[int64]int), slow: time.Millisecond}
	p := New(src, sink, Config{Workers: 2, RawQueueSize: 8, EventQueueSize: 8})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	rl, rc, el, ec := p.QueueStats()
	if rl > rc || el > ec {
		t.Fatalf("fila excedeu capacidade: raw %d/%d events %d/%d", rl, rc, el, ec)
	}
	// O leitor deve estar muito à frente do agregador apenas pelo tamanho
	// das filas + itens em mãos dos workers.
	inFlight := src.sent.Load() - p.Stats.Aggregated.Load()
	if max := int64(rc + ec + 2*2 + 1); inFlight > max {
		t.Errorf("itens em trânsito = %d, máximo esperado %d", inFlight, max)
	}
	cancel()
	<-done
}
