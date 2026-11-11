// Comando benchgo: implementação Go do benchmark de parsing/agregação.
//
// A implementação equivalente em Python está em bench/python/aggregate.py.
// As duas aplicam EXATAMENTE as mesmas regras e produzem a mesma saída JSON
// canônica (stdout), que o script bench/run.sh compara byte a byte. As
// medições vão para stderr.
//
//	benchgo -generate 1000000 -out dataset.jsonl   # gera dataset determinístico
//	benchgo -in dataset.jsonl                        # 1 goroutine (comparação justa)
//	benchgo -in dataset.jsonl -workers 4             # pool de goroutines + channels
package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Regras de validação (espelhadas em aggregate.py)
// ---------------------------------------------------------------------------

type record struct {
	TS          *float64 `json:"ts"`
	RemoteAddr  *string  `json:"remote_addr"`
	Method      *string  `json:"method"`
	URI         *string  `json:"uri"`
	Status      *int64   `json:"status"`
	BytesSent   *int64   `json:"bytes_sent"`
	RequestTime *float64 `json:"request_time"`
}

type agg struct {
	valid, invalid int64
	bytes, sumMs   int64
	byCode         map[int64]int64
	endpoints      map[string]int64
	ips            map[string]int64
}

func newAgg() *agg {
	return &agg{byCode: map[int64]int64{}, endpoints: map[string]int64{}, ips: map[string]int64{}}
}

func (a *agg) add(line []byte) {
	var r record
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &r) != nil ||
		r.TS == nil || r.RemoteAddr == nil || r.Method == nil || r.URI == nil ||
		r.Status == nil || r.BytesSent == nil || r.RequestTime == nil ||
		*r.TS <= 0 || *r.Status < 100 || *r.Status > 599 || *r.BytesSent < 0 || *r.RequestTime < 0 {
		a.invalid++
		return
	}
	if _, err := netip.ParseAddr(*r.RemoteAddr); err != nil {
		a.invalid++
		return
	}
	uri := *r.URI
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	a.valid++
	a.bytes += *r.BytesSent
	// A conversão explícita impede fusão em FMA (spec do Go), garantindo o
	// mesmo arredondamento IEEE-754 do Python.
	a.sumMs += int64(float64(*r.RequestTime*1000) + 0.5)
	a.byCode[*r.Status]++
	a.endpoints[*r.Method+" "+uri]++
	a.ips[*r.RemoteAddr]++
}

func (a *agg) merge(b *agg) {
	a.valid += b.valid
	a.invalid += b.invalid
	a.bytes += b.bytes
	a.sumMs += b.sumMs
	for k, v := range b.byCode {
		a.byCode[k] += v
	}
	for k, v := range b.endpoints {
		a.endpoints[k] += v
	}
	for k, v := range b.ips {
		a.ips[k] += v
	}
}

func top(m map[string]int64, n int) [][]any {
	type kv struct {
		k string
		v int64
	}
	s := make([]kv, 0, len(m))
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	slices.SortFunc(s, func(a, b kv) int {
		if c := cmp.Compare(b.v, a.v); c != 0 {
			return c
		}
		return cmp.Compare(a.k, b.k)
	})
	out := [][]any{}
	for i := 0; i < len(s) && i < n; i++ {
		out = append(out, []any{s[i].k, s[i].v})
	}
	return out
}

func (a *agg) result() map[string]any {
	classes := map[string]int64{"1xx": 0, "2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0}
	byCode := map[string]int64{}
	for c, n := range a.byCode {
		classes[strconv.FormatInt(c/100, 10)+"xx"] += n
		byCode[strconv.FormatInt(c, 10)] = n
	}
	return map[string]any{
		"events_valid":   a.valid,
		"events_invalid": a.invalid,
		"bytes_sent":     a.bytes,
		"sum_request_ms": a.sumMs,
		"classes":        classes,
		"by_code":        byCode,
		"distinct_ips":   len(a.ips),
		"top_endpoints":  top(a.endpoints, 10),
		"top_ips":        top(a.ips, 10),
	}
}

// ---------------------------------------------------------------------------

func main() {
	gen := flag.Int("generate", 0, "gera N linhas e sai")
	out := flag.String("out", "dataset.jsonl", "arquivo de saída do gerador")
	in := flag.String("in", "", "dataset de entrada")
	workers := flag.Int("workers", 1, "goroutines de parsing (1 = sequencial)")
	noop := flag.Bool("noop", false, "sai imediatamente (mede inicialização)")
	flag.Parse()

	switch {
	case *noop:
		return
	case *gen > 0:
		if err := generate(*gen, *out); err != nil {
			fatal(err)
		}
		return
	case *in == "":
		flag.Usage()
		os.Exit(2)
	}

	start := time.Now()
	f, err := os.Open(*in)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)

	total := newAgg()
	if *workers <= 1 {
		for sc.Scan() {
			total.add(sc.Bytes())
		}
	} else {
		// Leitor → channel de lotes → N workers com agregados locais → merge.
		// Lotes reduzem o custo de sincronização por linha.
		batches := make(chan [][]byte, *workers*2)
		partials := make([]*agg, *workers)
		var wg sync.WaitGroup
		for i := range *workers {
			partials[i] = newAgg()
			wg.Go(func() {
				for b := range batches {
					for _, l := range b {
						partials[i].add(l)
					}
				}
			})
		}
		batch := make([][]byte, 0, 1024)
		for sc.Scan() {
			batch = append(batch, slices.Clone(sc.Bytes()))
			if len(batch) == cap(batch) {
				batches <- batch
				batch = make([][]byte, 0, 1024)
			}
		}
		batches <- batch
		close(batches)
		wg.Wait()
		for _, p := range partials {
			total.merge(p)
		}
	}
	if err := sc.Err(); err != nil {
		fatal(err)
	}
	res := total.result()
	elapsed := time.Since(start)

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		fatal(err)
	}

	events := total.valid + total.invalid
	user, sys, rss := rusage()
	m := map[string]any{
		"lang": "go", "version": runtime.Version(), "workers": *workers,
		"events": events, "elapsed_s": round(elapsed.Seconds()),
		"events_per_s": int64(float64(events) / elapsed.Seconds()),
		"cpu_user_s":   round(user), "cpu_sys_s": round(sys), "max_rss_kb": rss,
	}
	b, _ := json.Marshal(m)
	fmt.Fprintln(os.Stderr, string(b))
}

func round(x float64) float64 { return float64(int64(x*1000+0.5)) / 1000 }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "benchgo:", err)
	os.Exit(1)
}

// generate cria um dataset determinístico (mesma semente → mesmo arquivo),
// parecido com o log do NGINX, incluindo ~1% de linhas inválidas.
func generate(n int, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	r := rand.New(rand.NewPCG(42, 2026))
	paths := make([]string, 200)
	for i := range paths {
		paths[i] = fmt.Sprintf("/app/section-%d/item-%d", i%17, i)
	}
	paths[0], paths[1], paths[2] = "/", "/api/items", "/error"
	methods := []string{"GET", "GET", "GET", "GET", "POST", "PUT", "DELETE"}
	statuses := []int{200, 200, 200, 200, 200, 200, 200, 201, 204, 301, 304, 400, 404, 404, 500, 502, 503}
	ts := 1_794_000_000.0
	for i := range n {
		ts += r.Float64() * 0.01
		if i%100 == 37 { // ~1% inválidas, de tipos variados
			switch (i / 100) % 4 {
			case 0:
				fmt.Fprintf(w, "{\"ts\":%.3f,\"remote_addr\":\"10.0.0.1\",\"meth\n", ts)
			case 1:
				fmt.Fprintf(w, "{\"ts\":%.3f,\"remote_addr\":\"10.0.0.1\",\"method\":\"GET\",\"uri\":\"/\",\"bytes_sent\":1,\"request_time\":0.001}\n", ts)
			case 2:
				fmt.Fprintf(w, "{\"ts\":%.3f,\"remote_addr\":\"10.0.0.1\",\"method\":\"GET\",\"uri\":\"/\",\"status\":999,\"bytes_sent\":1,\"request_time\":0.001}\n", ts)
			case 3:
				fmt.Fprintf(w, "texto que não é JSON %d\n", i)
			}
			continue
		}
		// Popularidade não uniforme (alguns caminhos e IPs dominam).
		p := paths[min(r.IntN(200), r.IntN(200))]
		if r.IntN(10) == 0 {
			p += "?page=" + strconv.Itoa(r.IntN(50))
		}
		ipN := min(r.IntN(5000), r.IntN(5000))
		ip := fmt.Sprintf("10.%d.%d.%d", ipN/65536, (ipN/256)%256, ipN%256)
		fmt.Fprintf(w, `{"ts":%.3f,"time":"2026-11-10T00:00:00+00:00","remote_addr":"%s","method":"%s","uri":"%s","status":%d,"bytes_sent":%d,"request_time":%.3f}`+"\n",
			ts, ip, methods[r.IntN(len(methods))], p, statuses[r.IntN(len(statuses))], 200+r.IntN(50_000), r.Float64()*r.Float64()*2)
	}
	return w.Flush()
}
