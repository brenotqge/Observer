package tailer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const poll = 10 * time.Millisecond

type harness struct {
	t      *testing.T
	path   string
	out    chan []byte
	cancel context.CancelFunc
	done   chan error
	tl     *Tailer
}

func start(t *testing.T, path string, cfg Config) *harness {
	t.Helper()
	cfg.Path = path
	cfg.PollInterval = poll
	tl := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{t: t, path: path, out: make(chan []byte, 100), cancel: cancel, done: make(chan error, 1), tl: tl}
	go func() { h.done <- tl.Run(ctx, h.out) }()
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Errorf("Run retornou erro: %v", err)
		}
		h.done <- nil // permite chamadas repetidas
	case <-time.After(2 * time.Second):
		h.t.Fatal("tailer não encerrou após cancelamento")
	}
}

func (h *harness) expect(lines ...string) {
	h.t.Helper()
	for _, want := range lines {
		select {
		case got := <-h.out:
			if string(got) != want {
				h.t.Fatalf("linha = %q, quer %q", got, want)
			}
		case <-time.After(2 * time.Second):
			h.t.Fatalf("timeout esperando %q", want)
		}
	}
}

func (h *harness) expectNothing(d time.Duration) {
	h.t.Helper()
	select {
	case got := <-h.out:
		h.t.Fatalf("linha inesperada: %q", got)
	case <-time.After(d):
	}
}

// waitOpen aguarda o tailer abrir o arquivo (e posicionar o offset).
func (h *harness) waitOpen() {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !h.tl.Stats.FilePresent.Load() {
		if time.Now().After(deadline) {
			h.t.Fatal("arquivo não foi aberto")
		}
		time.Sleep(poll)
	}
	time.Sleep(3 * poll)
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestOnlyNewLinesByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "antiga-1\nantiga-2\n")
	h := start(t, path, Config{})
	h.waitOpen()
	appendTo(t, path, "nova-1\nnova-2\n")
	h.expect("nova-1", "nova-2")
}

func TestFromStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "a\nb\n")
	h := start(t, path, Config{FromStart: true})
	h.expect("a", "b")
}

func TestIncompleteLineIsBuffered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	h := start(t, path, Config{})
	h.waitOpen()

	appendTo(t, path, `{"parcial":`)
	h.expectNothing(10 * poll) // fragmento não pode ser emitido
	appendTo(t, path, "true}\r\n")
	h.expect(`{"parcial":true}`) // \r também é removido
}

func TestMissingFileThenCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	h := start(t, path, Config{})
	time.Sleep(5 * poll)
	if h.tl.Stats.FilePresent.Load() {
		t.Fatal("arquivo não deveria estar presente")
	}
	// Arquivo criado depois: é novo, então é lido desde o início.
	appendTo(t, path, "primeira\n")
	h.expect("primeira")
}

func TestTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	h := start(t, path, Config{})
	h.waitOpen()
	appendTo(t, path, "linha-longa-antes-do-truncamento\n")
	h.expect("linha-longa-antes-do-truncamento")

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * poll)
	appendTo(t, path, "depois\n")
	h.expect("depois")
	if h.tl.Stats.Truncations.Load() != 1 {
		t.Errorf("Truncations = %d", h.tl.Stats.Truncations.Load())
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	appendTo(t, path, "")
	h := start(t, path, Config{})
	h.waitOpen()
	appendTo(t, path, "antes\n")
	h.expect("antes")

	// Rotação estilo logrotate: renomeia e cria um novo arquivo.
	if err := os.Rename(path, filepath.Join(dir, "access.log.1")); err != nil {
		t.Skipf("rename com arquivo aberto não suportado nesta plataforma: %v", err)
	}
	appendTo(t, filepath.Join(dir, "access.log.1"), "final-do-antigo\n")
	appendTo(t, path, "novo-1\n")
	h.expect("final-do-antigo", "novo-1")
	if h.tl.Stats.Rotations.Load() != 1 {
		t.Errorf("Rotations = %d", h.tl.Stats.Rotations.Load())
	}
}

func TestOversizedLineDiscarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	h := start(t, path, Config{MaxLineBytes: 300})
	h.waitOpen()
	// Linha enorme escrita em duas partes (atravessa o buffer pendente).
	appendTo(t, path, strings.Repeat("x", 200))
	time.Sleep(3 * poll)
	appendTo(t, path, strings.Repeat("y", 200)+"\nok\n"+strings.Repeat("z", 400)+"\nok2\n")
	h.expect("ok", "ok2")
	if got := h.tl.Stats.OversizedLines.Load(); got != 2 {
		t.Errorf("OversizedLines = %d, quer 2", got)
	}
}

// Backpressure: com o consumidor parado, Run deve bloquear no envio e ainda
// assim encerrar prontamente quando o contexto é cancelado.
func TestShutdownWhileBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, strings.Repeat("l\n", 50))
	tl := New(Config{Path: path, PollInterval: poll, FromStart: true})
	out := make(chan []byte) // sem buffer e sem leitor
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tl.Run(ctx, out) }()
	time.Sleep(5 * poll)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run não respeitou o cancelamento enquanto bloqueado")
	}
}
