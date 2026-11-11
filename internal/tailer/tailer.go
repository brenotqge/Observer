// Package tailer acompanha continuamente um arquivo de log (equivalente a
// `tail -F`), usando apenas a biblioteca padrão e polling.
//
// Garantias:
//   - Só emite linhas completas (terminadas em '\n'); fragmentos ficam em
//     buffer até a escrita ser concluída.
//   - Linhas maiores que MaxLineBytes são descartadas e contabilizadas.
//   - Ausência temporária do arquivo não encerra o leitor.
//   - Truncamento (tamanho < posição atual) reinicia a leitura do início.
//   - Rotação (o caminho passa a apontar para outro arquivo) é detectada; o
//     arquivo antigo é lido até o fim antes da troca.
//   - Envio bloqueante para o channel de saída: se o pipeline estiver
//     sobrecarregado, o leitor simplesmente para de ler (backpressure). O
//     próprio arquivo funciona como buffer; o NGINX nunca é bloqueado.
package tailer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// Config parametriza o Tailer.
type Config struct {
	Path         string
	PollInterval time.Duration
	MaxLineBytes int
	// FromStart=true lê o conteúdo já existente. Por padrão apenas eventos
	// novos (após a inicialização) são processados.
	FromStart bool
	Logger    *slog.Logger
}

// Stats são contadores seguros para leitura concorrente.
type Stats struct {
	LinesRead      atomic.Int64
	BytesRead      atomic.Int64
	OversizedLines atomic.Int64
	Truncations    atomic.Int64
	Rotations      atomic.Int64
	FilePresent    atomic.Bool
}

// Tailer lê linhas novas de um arquivo.
type Tailer struct {
	cfg   Config
	Stats Stats
}

// New cria um Tailer com valores padrão razoáveis.
func New(cfg Config) *Tailer {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.MaxLineBytes <= 0 {
		cfg.MaxLineBytes = 16 * 1024
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Tailer{cfg: cfg}
}

// Run envia cada linha completa (sem '\n') para out até ctx ser cancelado.
// Não fecha out: o chamador é o dono do channel. Retorna nil quando o
// encerramento foi solicitado via ctx.
func (t *Tailer) Run(ctx context.Context, out chan<- []byte) error {
	log := t.cfg.Logger
	var (
		f          *os.File
		offset     int64
		startAtEnd = !t.cfg.FromStart
		missingLog bool
		lb         = lineBuffer{max: t.cfg.MaxLineBytes, stats: &t.Stats}
		chunk      = make([]byte, 32*1024)
	)
	defer func() {
		if f != nil {
			f.Close()
		}
	}()

	for {
		if f == nil {
			var err error
			f, err = os.Open(t.cfg.Path)
			if err != nil {
				t.Stats.FilePresent.Store(false)
				if !missingLog {
					log.Warn("arquivo de log indisponível; aguardando", "path", t.cfg.Path, "err", err)
					missingLog = true
				}
				// Se o arquivo não existia na inicialização, quando surgir
				// será um arquivo novo: deve ser lido desde o início.
				startAtEnd = false
				if !sleep(ctx, t.cfg.PollInterval) {
					return nil
				}
				continue
			}
			missingLog = false
			t.Stats.FilePresent.Store(true)
			offset = 0
			if startAtEnd {
				if offset, err = f.Seek(0, io.SeekEnd); err != nil {
					f.Close()
					f = nil
					return err
				}
			}
			startAtEnd = false
			lb.reset()
			log.Info("acompanhando arquivo de log", "path", t.cfg.Path, "offset", offset)
		}

		// Lê tudo o que estiver disponível.
		for {
			n, err := f.Read(chunk)
			if n > 0 {
				offset += int64(n)
				t.Stats.BytesRead.Add(int64(n))
				if !lb.feed(ctx, chunk[:n], out) {
					return nil // ctx cancelado durante o envio
				}
			}
			if err == io.EOF || n == 0 {
				break
			}
			if err != nil {
				log.Warn("erro de leitura; reabrindo", "err", err)
				f.Close()
				f = nil
				break
			}
		}
		if f == nil {
			continue
		}

		// Fim do arquivo: verificar truncamento e rotação.
		cur, errCur := f.Stat()
		onDisk, errDisk := os.Stat(t.cfg.Path)
		switch {
		case errCur != nil:
			f.Close()
			f = nil
			continue
		case errDisk != nil:
			// Arquivo removido/renomeado e o novo ainda não existe: mantemos
			// o descritor antigo (pode receber escritas até o reopen).
			if !errors.Is(errDisk, fs.ErrNotExist) {
				log.Warn("stat falhou", "err", errDisk)
			}
		case !os.SameFile(cur, onDisk):
			// Rotação: o antigo já foi lido até EOF.
			t.Stats.Rotations.Add(1)
			if lb.pending() > 0 {
				log.Warn("descartando linha incompleta no arquivo rotacionado", "bytes", lb.pending())
			}
			log.Info("rotação detectada; abrindo novo arquivo")
			f.Close()
			f = nil
			continue // abrir o novo imediatamente (desde o início)
		case cur.Size() < offset:
			t.Stats.Truncations.Add(1)
			log.Info("truncamento detectado; relendo do início", "old_offset", offset, "size", cur.Size())
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				f.Close()
				f = nil
				continue
			}
			offset = 0
			lb.reset()
			continue
		}

		if !sleep(ctx, t.cfg.PollInterval) {
			return nil
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-tm.C:
		return true
	}
}

// lineBuffer monta linhas completas a partir de blocos arbitrários.
type lineBuffer struct {
	buf        []byte
	max        int
	discarding bool // a linha atual excedeu o limite; ignorar até o '\n'
	stats      *Stats
}

func (l *lineBuffer) reset()       { l.buf = l.buf[:0]; l.discarding = false }
func (l *lineBuffer) pending() int { return len(l.buf) }

// feed processa um bloco; retorna false se ctx foi cancelado.
func (l *lineBuffer) feed(ctx context.Context, data []byte, out chan<- []byte) bool {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if !l.discarding {
				l.buf = append(l.buf, data...)
				if len(l.buf) > l.max {
					l.stats.OversizedLines.Add(1)
					l.discarding = true
					l.buf = l.buf[:0]
				}
			}
			return true
		}
		part := data[:i]
		data = data[i+1:]
		if l.discarding {
			l.discarding = false
			continue
		}
		if len(l.buf)+len(part) > l.max {
			l.stats.OversizedLines.Add(1)
			l.buf = l.buf[:0]
			continue
		}
		// Cópia própria: o bloco de leitura é reutilizado.
		line := make([]byte, 0, len(l.buf)+len(part))
		line = append(line, l.buf...)
		line = append(line, part...)
		l.buf = l.buf[:0]
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 {
			continue
		}
		l.stats.LinesRead.Add(1)
		select {
		case out <- line:
		case <-ctx.Done():
			return false
		}
	}
	return true
}
