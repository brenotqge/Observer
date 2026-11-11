package logparse

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	line := `{"ts":1794347892.972,"time":"2026-11-10T21:58:12+00:00","remote_addr":"203.0.113.7","method":"GET","uri":"/slow","status":200,"bytes_sent":49408,"request_time":1.009}`
	ev, err := Parse([]byte(line))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	want := time.Unix(1794347892, 972*int64(time.Millisecond))
	if !ev.Time.Equal(want) {
		t.Errorf("Time = %v, quer %v", ev.Time, want)
	}
	if ev.RemoteAddr.String() != "203.0.113.7" || ev.Method != "GET" || ev.Path != "/slow" ||
		ev.Status != 200 || ev.BytesSent != 49408 || ev.RequestTime != 1009*time.Millisecond {
		t.Errorf("evento incorreto: %+v", ev)
	}
}

func TestParseNormalization(t *testing.T) {
	cases := []struct {
		name, method, uri, wantMethod, wantPath string
	}{
		{"query residual removida", "GET", "/a?token=secret", "GET", "/a"},
		{"método vazio (400)", "", "", "UNKNOWN", "(invalid)"},
		{"método inválido", "get", "/", "UNKNOWN", "/"},
		{"caminho longo truncado", "GET", "/" + strings.Repeat("x", 1000), "GET", "/" + strings.Repeat("x", MaxPathLen-1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := `{"ts":1,"remote_addr":"::ffff:10.0.0.1","method":"` + c.method + `","uri":"` + c.uri +
				`","status":400,"bytes_sent":0,"request_time":0}`
			ev, err := Parse([]byte(line))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Method != c.wantMethod || ev.Path != c.wantPath {
				t.Errorf("got %q %q", ev.Method, ev.Path)
			}
			if ev.RemoteAddr.String() != "10.0.0.1" {
				t.Errorf("IPv4 mapeado em IPv6 deveria ser normalizado, got %s", ev.RemoteAddr)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	ok := `"ts":1,"remote_addr":"1.2.3.4","method":"GET","uri":"/","bytes_sent":1,"request_time":0`
	cases := []struct {
		name string
		line string
		want error
	}{
		{"vazia", "   ", ErrEmptyLine},
		{"não objeto", `[1,2]`, ErrNotJSONObject},
		{"texto puro", `GET / HTTP/1.1`, ErrNotJSONObject},
		{"JSON truncado", `{"ts":1,"remote_addr":"1.2`, ErrInvalidJSON},
		{"tipo errado", `{` + ok + `,"status":"200"}`, ErrInvalidJSON},
		{"sem status", `{` + ok + `}`, ErrMissingField},
		{"sem ts", `{"remote_addr":"1.2.3.4","method":"GET","uri":"/","status":200,"bytes_sent":1,"request_time":0}`, ErrMissingField},
		{"status fora da faixa", `{` + ok + `,"status":999}`, ErrInvalidField},
		{"IP inválido", `{"ts":1,"remote_addr":"not-an-ip","method":"GET","uri":"/","status":200,"bytes_sent":1,"request_time":0}`, ErrInvalidField},
		{"bytes negativos", `{"ts":1,"remote_addr":"1.2.3.4","method":"GET","uri":"/","status":200,"bytes_sent":-1,"request_time":0}`, ErrInvalidField},
		{"ts negativo", `{"ts":-5,"remote_addr":"1.2.3.4","method":"GET","uri":"/","status":200,"bytes_sent":1,"request_time":0}`, ErrInvalidField},
		{"request_time negativo", `{"ts":1,"remote_addr":"1.2.3.4","method":"GET","uri":"/","status":200,"bytes_sent":1,"request_time":-1}`, ErrInvalidField},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.line))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, quer %v", err, c.want)
			}
		})
	}
}

// TestParseRealNginxLog processa um log gerado pelo NGINX real do
// compose.yaml (testdata/nginx_access_real.jsonl), incluindo uma requisição
// malformada (400, método/URI vazios) e uma com query string.
func TestParseRealNginxLog(t *testing.T) {
	f, err := os.Open("../../testdata/nginx_access_real.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var n int
	codes := map[int]int{}
	for sc.Scan() {
		ev, err := Parse(sc.Bytes())
		if err != nil {
			t.Fatalf("linha %d: %v", n+1, err)
		}
		if strings.Contains(ev.Path, "?") {
			t.Errorf("query string vazou para o caminho: %q", ev.Path)
		}
		codes[ev.Status]++
		n++
	}
	if n < 10 {
		t.Fatalf("fixture com poucas linhas: %d", n)
	}
	for _, c := range []int{200, 404, 500, 400} {
		if codes[c] == 0 {
			t.Errorf("esperava ao menos um status %d no log real", c)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"ts":1,"remote_addr":"1.2.3.4","method":"GET","uri":"/","status":200,"bytes_sent":1,"request_time":0}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, b []byte) {
		ev, err := Parse(b) // não pode entrar em pânico
		if err == nil && (ev.Status < 100 || ev.Status > 599 || ev.BytesSent < 0) {
			t.Fatalf("evento inválido aceito: %+v", ev)
		}
	})
}

func BenchmarkParse(b *testing.B) {
	line := []byte(`{"ts":1794347892.972,"time":"2026-11-10T21:58:12+00:00","remote_addr":"203.0.113.7","method":"GET","uri":"/api/items","status":200,"bytes_sent":213,"request_time":0.000}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(line); err != nil {
			b.Fatal(err)
		}
	}
}
