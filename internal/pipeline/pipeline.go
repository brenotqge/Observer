// Package pipeline conecta leitura, parsing e agregação com goroutines e
// channels de capacidade limitada:
//
//	Tailer (1 goroutine) ──raw []byte──▶ Parsers (N goroutines) ──Event──▶ Agregador (1 goroutine) ──▶ Store
//
// Propriedade dos channels: quem envia é quem fecha. O tailer encerra ao
// cancelar o contexto, o channel raw é fechado, os workers drenam o que
// restou e terminam, o channel de eventos é fechado e o agregador termina
// após contabilizar tudo. Assim nenhuma linha já lida é perdida no
// desligamento e nenhuma é contada duas vezes.
//
// Sobrecarga: os channels são bloqueantes. Se o agregador ou os parsers não
// acompanharem, o tailer deixa de ler e as linhas aguardam no arquivo
// (backpressure). O NGINX não é afetado, pois escreve no arquivo, não no
// Observer. A defasagem fica visível em queue_len/queue_cap.
package pipeline

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"gonginx-observer/internal/logparse"
)

// Source produz linhas brutas (implementado por *tailer.Tailer).
type Source interface {
	Run(ctx context.Context, out chan<- []byte) error
}

// Sink consome eventos validados (implementado por *metrics.Store).
type Sink interface {
	Add(logparse.Event)
}

// Config parametriza o pipeline.
type Config struct {
	Workers        int
	RawQueueSize   int
	EventQueueSize int
	Logger         *slog.Logger
}

// Stats expõe contadores do pipeline.
type Stats struct {
	Parsed      atomic.Int64
	ParseErrors atomic.Int64
	Aggregated  atomic.Int64
}

// Pipeline é uma instância configurada.
type Pipeline struct {
	cfg    Config
	src    Source
	sink   Sink
	raw    chan []byte
	events chan logparse.Event
	Stats  Stats
}

// New cria o pipeline e seus channels.
func New(src Source, sink Sink, cfg Config) *Pipeline {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.RawQueueSize <= 0 {
		cfg.RawQueueSize = 1024
	}
	if cfg.EventQueueSize <= 0 {
		cfg.EventQueueSize = 1024
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Pipeline{
		cfg:    cfg,
		src:    src,
		sink:   sink,
		raw:    make(chan []byte, cfg.RawQueueSize),
		events: make(chan logparse.Event, cfg.EventQueueSize),
	}
}

// QueueStats retorna ocupação e capacidade dos channels (len/cap são
// seguros para leitura concorrente).
func (p *Pipeline) QueueStats() (rawLen, rawCap, evLen, evCap int) {
	return len(p.raw), cap(p.raw), len(p.events), cap(p.events)
}

// Workers retorna o tamanho do pool.
func (p *Pipeline) Workers() int { return p.cfg.Workers }

// Run executa o pipeline até ctx ser cancelado e todo o trabalho em
// andamento ser drenado. Retorna o erro do Source, se houver.
func (p *Pipeline) Run(ctx context.Context) error {
	var srcErr error

	// Estágio 1: leitor. Único remetente de raw, portanto fecha raw.
	go func() {
		defer close(p.raw)
		srcErr = p.src.Run(ctx, p.raw)
	}()

	// Estágio 2: pool fixo de parsers (parsing JSON é o trabalho de CPU).
	var workers sync.WaitGroup
	for range p.cfg.Workers {
		workers.Go(p.parseWorker)
	}
	// Quando todos os workers terminarem, nenhum outro envio ocorrerá.
	go func() {
		workers.Wait()
		close(p.events)
	}()

	// Estágio 3: agregador — único escritor do Store.
	for ev := range p.events {
		p.sink.Add(ev)
		p.Stats.Aggregated.Add(1)
	}
	// Leitura de srcErr é segura: close(raw) acontece após a atribuição,
	// e o range acima só termina depois disso (happens-before via channels).
	return srcErr
}

func (p *Pipeline) parseWorker() {
	for line := range p.raw {
		ev, err := logparse.Parse(line)
		if err != nil {
			n := p.Stats.ParseErrors.Add(1)
			// Log com limitação de volume; nunca registra o conteúdo da
			// linha (pode conter dados pessoais).
			if n <= 10 || n%1000 == 0 {
				p.cfg.Logger.Warn("linha de log inválida ignorada", "err", err, "bytes", len(line), "total_errors", n)
			}
			continue
		}
		p.Stats.Parsed.Add(1)
		// Envio sem select: o agregador só para depois que events é
		// fechado, então este envio sempre progride.
		p.events <- ev
	}
}
