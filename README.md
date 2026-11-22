# GoNginx Observer

Painel acadêmico para observar acessos, erros e alertas de quatro sites
executados em portas separadas com NGINX Open Source.

## Demonstração rápida

1. Inicie os serviços: `docker compose up --build`
2. Abra o Observer: http://localhost:8080
3. Acesse um dos quatro sites: http://localhost:8081 a http://localhost:8084
4. Clique em **Gerar testes deste site** em qualquer página
5. Observe o resultado por 30 segundos no modo leigo ou avançado do Observer

O modo leigo usa palavras simples como “acessos” e “páginas com problema”.
O modo avançado mostra status HTTP, IPs, filas, goroutines e detalhes técnicos.

## Links principais

| Recurso | Endereço | Finalidade |
|---|---|---|
| Observer | http://localhost:8080 | Dashboard, busca, filtros e API |
| Site 1 | http://localhost:8081 | Página principal e endpoints básicos |
| Site 2 | http://localhost:8082 | Página independente com `/api`, `/slow` e `/error` |
| Site 3 | http://localhost:8083 | Página independente com `/api`, `/slow` e `/error` |
| Site 4 | http://localhost:8084 | Página independente com `/api`, `/slow` e `/error` |

**Documentação completa:** consulte a seção
[Documentação](#documentação) para acessar arquitetura, comparação de linguagens,
roteiro do vídeo, roadmap e licenças.

Serviço **independente, escrito em Go**, que estende a observabilidade do
**NGINX Open Source**: lê os access logs estruturados (JSON Lines), processa os
eventos em um pipeline concorrente de goroutines e channels, calcula métricas
HTTP em tempo real, detecta anomalias simples e expõe tudo por uma API REST e
um dashboard web.

Projeto acadêmico para a disciplina de Linguagens de Programação.

## Problema

O NGINX Open Source registra cada requisição em log, mas não oferece um painel
de métricas em tempo real, rankings ou alertas — recursos presentes na edição
comercial NGINX Plus. Este projeto implementa uma **camada própria** de
observabilidade usando apenas o que o NGINX Open Source já produz, **sem
modificar o código do NGINX** e sem usar nada do NGINX Plus. O Observer roda
ao lado do NGINX como um *sidecar*.

## Arquitetura

```
 Clientes HTTP ──▶ NGINX Open Source ──▶ access.jsonl (volume Docker compartilhado)
                                                │  (montado somente leitura)
                                                ▼
 ┌────────────────────────── Go Observer ─────────────────────────────────┐
 │  Tailer ──chan []byte──▶ Parsers (pool N) ──chan Event──▶ Agregador    │
 │  (1 goroutine)          (N goroutines)                   (1 goroutine) │
 │                                                              │         │
 │                                     Motor de alertas ◀── Store ──▶ API REST + Dashboard
 │                                     (1 goroutine, 1 s)   (memória)   (net/http)
 └────────────────────────────────────────────────────────────────────────┘
```

Detalhes, decisões e trade-offs: [ARCHITECTURE.md](ARCHITECTURE.md).

## Pré-requisitos

- Docker com Docker Compose v2 (Docker Desktop no Windows/macOS ou Docker Engine no Linux).
- Opcional: Go 1.25+ para rodar testes/loadgen fora de containers. **Não é obrigatório**:
  o script `scripts/go-docker.sh` executa a toolchain Go dentro da imagem oficial `golang`.
- Opcional: Bash (Linux, WSL ou Git Bash) para os scripts de teste e benchmark.

## Como executar

```bash
docker compose up --build
```

| Serviço   | URL (somente localhost)  | Papel                                  |
|-----------|--------------------------|----------------------------------------|
| nginx 1   | http://localhost:8081    | site 1 + endpoints de demonstração     |
| nginx 2   | http://localhost:8082    | site 2 estático + `/api`, `/slow`, `/error` |
| nginx 3   | http://localhost:8083    | site 3 estático + `/api`, `/slow`, `/error` |
| nginx 4   | http://localhost:8084    | site 4 estático + `/api`, `/slow`, `/error` |
| observer  | http://localhost:8080    | dashboard, filtros e API |

Endpoints de demonstração do NGINX: `/` (200), `/api/items` (200),
`/slow` (200 em ~1 s), `/error` (500), qualquer outro caminho (404).

Para parar: `docker compose down` (mantém o volume de logs) ou
`docker compose down -v` (apaga também o log).

## Como gerar tráfego

O gerador (`cmd/loadgen`) é um comando Go independente. Ele só aceita destinos
locais/privados e usa **IPs fictícios** das faixas de documentação TEST-NET
(RFC 5737) no cabeçalho `X-Forwarded-For`.

Pelo Compose (não precisa de Go instalado):

```bash
docker compose run --rm loadgen -scenario normal -n 2000 -c 10 -rate 100
```

```bash
docker compose run --rm loadgen -scenario spike -n 400
```

```bash
docker compose run --rm loadgen -scenario errors -n 1500
```

Com Go instalado no host:

```bash
go run ./cmd/loadgen -url http://localhost:8081 -scenario normal -n 2000
```

| Cenário  | Efeito                                                         |
|----------|----------------------------------------------------------------|
| `normal` | mistura realista (200/404/500/lento), 20 IPs                   |
| `spike`  | um único IP faz tudo → dispara a **regra A** (> 300 req/60 s)  |
| `errors` | 60% de respostas 500 → dispara a **regra B** (> 20% de 5xx)    |

Opções: `-url`, `-n` (total), `-c` (concorrência), `-rate` (req/s),
`-ips` (quantidade de IPs fictícios), `-mix` (distribuição personalizada, ex.:
`home=50,api=30,notfound=10,error=5,slow=5`). Veja `-h`.

## Como consultar a API

| Método e rota                 | Conteúdo                                                        |
|-------------------------------|-----------------------------------------------------------------|
| `GET /healthz`                | estado do processo e presença do arquivo de log                 |
| `GET /api/v1/metrics`         | acumulado, janela de 60 s, histórico por segundo, pipeline e runtime |
| `GET /api/v1/status-codes`    | contagem por classe (2xx…5xx) e por código                      |
| `GET /api/v1/top-endpoints`   | endpoints mais acessados (`?limit=1..100`, padrão 10)           |
| `GET /api/v1/top-ips`         | IPs mais ativos (`?limit=1..100`), mascarados conforme config   |
| `GET /api/v1/alerts`          | alertas recentes (`?limit=1..1000`) e regras configuradas       |

```bash
curl -s http://localhost:8080/api/v1/metrics
```

Exemplo resumido de `/api/v1/metrics`:

```json
{
  "generated_at": "2026-11-10T21:57:37Z",
  "uptime_seconds": 14.1,
  "cumulative": { "total_requests": 23, "status_classes": {"1xx":0,"2xx":21,"3xx":0,"4xx":1,"5xx":1},
                  "error_rate": 0.087, "server_error_rate": 0.043, "avg_response_ms": 0, "bytes_sent": 90680 },
  "window": { "seconds": 60, "requests": 23, "requests_per_second": 1.63, "error_rate": 0.087, "distinct_ips": 2 },
  "history": [ { "t": 1794347797, "requests": 0, "errors_4xx": 0, "errors_5xx": 0 } ],
  "pipeline": { "workers": 4, "lines_read": 23, "parsed": 23, "parse_errors": 0,
                "raw_queue": {"len":0,"cap":1024}, "event_queue": {"len":0,"cap":1024} },
  "runtime": { "go_version": "go1.25.14", "goroutines": 14, "heap_alloc_bytes": 571616 }
}
```

Todas as rotas retornam JSON válido mesmo sem nenhuma requisição registrada.
**Métricas acumuladas** valem desde a inicialização do processo; **métricas de
janela** cobrem os últimos 60 s (buckets de 1 s).

## Dashboard
Abra **http://localhost:8080/**.

O frontend é HTML, CSS e JavaScript sem dependências, embutido no binário Go
com `go:embed`. Ele consulta a API a cada segundo e oferece:

- modo leigo e modo avançado;
- busca por páginas, IPs e alertas;
- filtro por porta;
- filtro por tipo de resposta;
- período dos últimos 60 segundos ou todo o período;
- gráfico completo em todo o período;
- botão para limpar os filtros;
- lista de endpoints e alertas;
- estado das filas e goroutines no modo avançado.

## Configuração (variáveis de ambiente)

| Variável                      | Padrão               | Descrição |
|-------------------------------|----------------------|-----------|
| `OBSERVER_LISTEN_ADDR`        | `:8080`              | endereço HTTP |
| `OBSERVER_LOG_PATH`           | `/logs/access.jsonl` | arquivo JSON Lines do NGINX |
| `OBSERVER_READ_FROM_START`    | `false`              | processar conteúdo já existente |
| `OBSERVER_POLL_INTERVAL`      | `250ms`              | intervalo de verificação do arquivo |
| `OBSERVER_MAX_LINE_BYTES`     | `16384`              | linhas maiores são descartadas |
| `OBSERVER_WORKERS`            | `4`                  | goroutines de parsing (1..64) |
| `OBSERVER_RAW_QUEUE`          | `1024`               | capacidade do channel de linhas |
| `OBSERVER_EVENT_QUEUE`        | `1024`               | capacidade do channel de eventos |
| `OBSERVER_WINDOW_SECONDS`     | `60`                 | janela móvel |
| `OBSERVER_MAX_TRACKED_KEYS`   | `1000`               | limite de endpoints/IPs distintos em memória |
| `OBSERVER_ALERT_IP_THRESHOLD` | `300`                | regra A: req por IP na janela (0 desativa) |
| `OBSERVER_ALERT_5XX_RATE`     | `0.20`               | regra B: proporção de 5xx (0 desativa) |
| `OBSERVER_ALERT_MIN_SAMPLES`  | `50`                 | regra B: mínimo de requisições na janela |
| `OBSERVER_ALERT_COOLDOWN`     | `60s`                | intervalo mínimo entre alertas iguais |
| `OBSERVER_ALERT_INTERVAL`     | `1s`                 | frequência de avaliação das regras |
| `OBSERVER_ALERT_HISTORY`      | `100`                | alertas mantidos em memória |
| `OBSERVER_MASK_IPS`           | `partial`            | `none` ou `partial` (/24 IPv4, /48 IPv6) |
| `OBSERVER_LOG_LEVEL`          | `info`               | `debug`, `info`, `warn`, `error` |
| `OBSERVER_SHUTDOWN_TIMEOUT`   | `5s`                 | limite do desligamento gracioso |

O `compose.yaml` define `OBSERVER_MASK_IPS=none` porque a demonstração usa
IPs fictícios. As portas dos sites podem ser trocadas pelas variáveis
`SITE1_PORT`, `SITE2_PORT`, `SITE3_PORT`,
`SITE4_PORT` e `OBSERVER_PORT`.

## Como executar os testes

Com Go instalado:

```bash
go test ./...
```

```bash
go test -race ./...
```

Sem Go no host (usa o container `golang:1.25-bookworm`, que inclui gcc para o race detector):

```bash
./scripts/go-docker.sh sh -c 'gofmt -l . && go vet ./... && go test ./... && go test -race ./...'
```

Teste de integração ponta a ponta (sobe o Compose, gera tráfego, verifica API,
dashboard, alertas e desligamento gracioso):

```bash
./scripts/integration-test.sh
```

Benchmark Go × Python (gera `bench/results/RESULTS.md`):

```bash
./bench/run.sh
```

## Retenção de dados e privacidade

- Métricas ficam **apenas em memória** e são zeradas ao reiniciar o Observer.
  Na inicialização, só eventos novos são processados.
- O log em disco pertence ao NGINX (volume `nginx-logs`) e cresce até ser
  rotacionado ou apagado (`docker compose down -v`).
- O formato de log não registra query string, cookies, cabeçalhos de
  autorização, User-Agent nem corpo das requisições.
- API e dashboard não têm autenticação e por isso são publicados apenas em
  `127.0.0.1`.
- Alertas são **heurísticos** e podem ser falsos positivos; nenhum bloqueio
  automático é realizado.

## Estrutura

```
cmd/observer      serviço principal          internal/tailer     leitura contínua do log
cmd/loadgen       gerador de tráfego          internal/logparse   parsing e validação
cmd/benchgo       benchmark (versão Go)       internal/pipeline   goroutines + channels
bench/            benchmark (Python, scripts) internal/metrics    agregação e janela móvel
nginx/            imagem/config do NGINX      internal/alerts     regras de anomalia
scripts/          testes e toolchain Docker   internal/api        REST + dashboard (web/)
testdata/         log real do NGINX           internal/app        ciclo de vida
```

## Documentação

- [ARCHITECTURE.md](ARCHITECTURE.md) — componentes, concorrência, decisões e evolução para cloud
- [GO_VS_OTHER_LANGUAGES.md](GO_VS_OTHER_LANGUAGES.md) — comparação técnica e benchmark
- [PRESENTATION.md](PRESENTATION.md) — roteiro da apresentação e da demonstração
- [ROADMAP.md](ROADMAP.md) — etapas, critérios de aceitação e pendências
- [LICENSES.md](LICENSES.md) — licenças e atribuições
