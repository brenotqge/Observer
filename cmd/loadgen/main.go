// Comando loadgen: gerador de tráfego de DEMONSTRAÇÃO para o NGINX local.
//
// Por segurança, só aceita destinos locais (localhost, loopback, redes
// privadas ou nomes de serviço Docker sem ponto, como "nginx"). Não use
// contra serviços públicos ou sistemas de terceiros.
//
// Os IPs de origem são FICTÍCIOS (faixas de documentação TEST-NET, RFC 5737)
// e enviados em X-Forwarded-For; o NGINX da demonstração confia nesse
// cabeçalho apenas quando a conexão vem de redes privadas.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// endpoints disponíveis no NGINX de demonstração.
var endpoints = map[string]func(r *rand.Rand) string{
	"home":     func(*rand.Rand) string { return "/" },
	"api":      func(*rand.Rand) string { return "/api/items" },
	"notfound": func(r *rand.Rand) string { return "/missing/page-" + strconv.Itoa(1+r.IntN(5)) },
	"error":    func(*rand.Rand) string { return "/error" },
	"slow":     func(*rand.Rand) string { return "/slow" },
}

// scenarios pré-definidos para a apresentação.
var scenarios = map[string]struct {
	mix  string
	ips  int
	desc string
}{
	"normal": {"home=50,api=30,notfound=12,error=3,slow=5", 20, "tráfego típico, poucos erros"},
	"errors": {"home=20,api=10,notfound=10,error=60", 20, "60% de respostas 500 → dispara a regra de 5xx"},
	"spike":  {"home=60,api=40", 1, "um único IP concentra tudo → dispara a regra por IP"},
}

type weighted struct {
	name   string
	weight int
}

func parseMix(s string) ([]weighted, int, error) {
	var out []weighted
	total := 0
	for part := range strings.SplitSeq(s, ",") {
		name, w, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, 0, fmt.Errorf("item %q: use nome=peso", part)
		}
		if _, known := endpoints[name]; !known {
			return nil, 0, fmt.Errorf("endpoint desconhecido %q", name)
		}
		n, err := strconv.Atoi(w)
		if err != nil || n < 0 {
			return nil, 0, fmt.Errorf("peso inválido em %q", part)
		}
		if n > 0 {
			out = append(out, weighted{name, n})
			total += n
		}
	}
	if total == 0 {
		return nil, 0, errors.New("distribuição vazia")
	}
	return out, total, nil
}

func pick(r *rand.Rand, mix []weighted, total int) string {
	x := r.IntN(total)
	for _, w := range mix {
		if x < w.weight {
			return w.name
		}
		x -= w.weight
	}
	return mix[len(mix)-1].name
}

// fakeIP gera endereços nas faixas de documentação (nunca roteáveis).
func fakeIP(i int) string {
	nets := []string{"192.0.2", "198.51.100", "203.0.113"}
	return fmt.Sprintf("%s.%d", nets[i%len(nets)], 10+(i/len(nets))%240)
}

// checkLocalTarget recusa destinos que não sejam claramente locais.
func checkLocalTarget(u *url.URL) error {
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "host.docker.internal" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() {
			return nil
		}
		return fmt.Errorf("IP %s não é local/privado", host)
	}
	if host != "" && !strings.Contains(host, ".") { // nome de serviço Docker
		return nil
	}
	return fmt.Errorf("destino %q não é local", host)
}

func main() {
	var (
		target   = flag.String("url", envOr("LOADGEN_URL", "http://localhost:8081"), "URL base do NGINX local (padrão: $LOADGEN_URL)")
		total    = flag.Int("n", 1000, "número total de requisições")
		conc     = flag.Int("c", 10, "concorrência (goroutines de envio)")
		scenario = flag.String("scenario", "normal", "normal | errors | spike")
		mixFlag  = flag.String("mix", "", "distribuição personalizada, ex.: home=50,api=30,notfound=10,error=5,slow=5")
		nIPs     = flag.Int("ips", 0, "quantidade de IPs fictícios (0 = padrão do cenário)")
		rate     = flag.Float64("rate", 0, "limite de requisições/s (0 = sem limite)")
		timeout  = flag.Duration("timeout", 10*time.Second, "timeout por requisição")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Uso: loadgen [flags]\n\nCenários:\n")
		for _, k := range slices.Sorted(maps.Keys(scenarios)) {
			s := scenarios[k]
			fmt.Fprintf(os.Stderr, "  %-7s %s (mix %s, %d IP(s))\n", k, s.desc, s.mix, s.ips)
		}
		fmt.Fprintln(os.Stderr, "\nFlags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	sc, ok := scenarios[*scenario]
	if !ok {
		fail("cenário desconhecido %q", *scenario)
	}
	if *mixFlag == "" {
		*mixFlag = sc.mix
	}
	if *nIPs <= 0 {
		*nIPs = sc.ips
	}
	mix, mixTotal, err := parseMix(*mixFlag)
	if err != nil {
		fail("mix: %v", err)
	}
	if *total < 1 || *conc < 1 || *conc > 1000 {
		fail("valores inválidos: -n >= 1 e 1 <= -c <= 1000")
	}
	base, err := url.Parse(strings.TrimRight(*target, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		fail("URL inválida: %s", *target)
	}
	if err := checkLocalTarget(base); err != nil {
		fail("recusado: %v. Este gerador é apenas para o ambiente local de demonstração.", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			MaxIdleConns:        *conc,
			MaxIdleConnsPerHost: *conc,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	// Produtor de trabalhos (com limitador de taxa opcional) → pool de
	// workers → contadores atômicos.
	jobs := make(chan int, *conc)
	go func() {
		defer close(jobs)
		var tick <-chan time.Time
		if *rate > 0 {
			t := time.NewTicker(time.Duration(float64(time.Second) / *rate))
			defer t.Stop()
			tick = t.C
		}
		for i := range *total {
			if tick != nil {
				select {
				case <-tick:
				case <-ctx.Done():
					return
				}
			}
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		mu       sync.Mutex
		byStatus = map[int]int{}
		failures atomic.Int64
		sent     atomic.Int64
	)
	fmt.Printf("loadgen: %d requisições, concorrência %d, cenário %q, %d IP(s) fictício(s) → %s\n",
		*total, *conc, *scenario, *nIPs, base)
	start := time.Now()
	var wg sync.WaitGroup
	for w := range *conc {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(w)+1, uint64(start.UnixNano())))
			for i := range jobs {
				path := endpoints[pick(r, mix, mixTotal)](r)
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+path, nil)
				req.Header.Set("X-Forwarded-For", fakeIP(i%*nIPs))
				req.Header.Set("User-Agent", "gonginx-loadgen/1.0")
				resp, err := client.Do(req)
				sent.Add(1)
				if err != nil {
					failures.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				mu.Lock()
				byStatus[resp.StatusCode]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	fmt.Printf("concluído em %s — %.0f req/s, falhas de conexão: %d\n",
		elapsed.Round(time.Millisecond), float64(sent.Load())/elapsed.Seconds(), failures.Load())
	codes := make([]int, 0, len(byStatus))
	for c := range byStatus {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	for _, c := range codes {
		fmt.Printf("  HTTP %d: %d\n", c, byStatus[c])
	}
	if failures.Load() > 0 && int(failures.Load()) == int(sent.Load()) {
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "loadgen: "+format+"\n", a...)
	os.Exit(2)
}
