package main

import (
	"math/rand/v2"
	"net/url"
	"testing"
)

func TestCheckLocalTarget(t *testing.T) {
	allowed := []string{"http://localhost:8081", "http://127.0.0.1", "http://[::1]:80", "http://nginx",
		"http://192.168.0.10", "http://10.1.2.3", "http://172.20.0.2", "http://host.docker.internal:8081", "http://app.localhost"}
	denied := []string{"http://example.com", "https://8.8.8.8", "http://nginx.org", "http://203.0.113.5"}
	for _, s := range allowed {
		u, _ := url.Parse(s)
		if err := checkLocalTarget(u); err != nil {
			t.Errorf("%s deveria ser aceito: %v", s, err)
		}
	}
	for _, s := range denied {
		u, _ := url.Parse(s)
		if checkLocalTarget(u) == nil {
			t.Errorf("%s deveria ser recusado", s)
		}
	}
}

func TestParseMixAndPick(t *testing.T) {
	if _, _, err := parseMix("home=1,desconhecido=2"); err == nil {
		t.Error("endpoint desconhecido deveria falhar")
	}
	if _, _, err := parseMix("home=0"); err == nil {
		t.Error("distribuição vazia deveria falhar")
	}
	mix, total, err := parseMix("home=75,error=25")
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(1, 2))
	counts := map[string]int{}
	for range 10_000 {
		counts[pick(r, mix, total)]++
	}
	if counts["error"] < 2200 || counts["error"] > 2800 {
		t.Errorf("distribuição fora do esperado: %v", counts)
	}
	for _, sc := range scenarios {
		if _, _, err := parseMix(sc.mix); err != nil {
			t.Errorf("cenário inválido %q: %v", sc.mix, err)
		}
	}
}

func TestFakeIPsAreDocumentationRanges(t *testing.T) {
	for i := range 100 {
		ip := fakeIP(i)
		u, _ := url.Parse("http://" + ip)
		if u.Hostname() != ip {
			t.Fatalf("IP inválido %q", ip)
		}
		switch ip[:7] {
		case "192.0.2", "198.51.", "203.0.1":
		default:
			t.Fatalf("%s fora das faixas TEST-NET", ip)
		}
	}
}
