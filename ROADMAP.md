# Roadmap — GoNginx Observer

Legenda: ✅ concluído e verificado · ⏳ pendente · 💡 melhoria opcional

## Fase 1 — Fundação ✅

| Etapa | Critério de aceitação | Verificação |
|---|---|---|
| ✅ Estrutura de diretórios e `go.mod` (Go 1.25, sem dependências) | `go build ./...` compila | `scripts/go-docker.sh go build ./...` |
| ✅ NGINX com config própria | `nginx -t` passa no build da imagem | `nginx/Dockerfile` |
| ✅ Endpoints 200/404/500 e `/slow` | respostas corretas | teste de integração |
| ✅ Log JSON Lines em volume próprio | linhas válidas com ts, IP, método, caminho, status, bytes, tempo | `testdata/nginx_access_real.jsonl` |
| ✅ Volume `:ro` no Observer, sem permissões amplas | Observer (uid 65532) lê arquivo `0644` | `/healthz` → `log_file_present: true` |
| ✅ `compose.yaml` + multi-stage build | `docker compose up --build` sobe dois serviços healthy | teste de integração |

## Fase 2 — Processamento ✅

| Etapa | Critério de aceitação | Verificação |
|---|---|---|
| ✅ Tailer | só linhas novas; linhas incompletas em buffer; arquivo ausente; truncamento; rotação; linhas gigantes descartadas | `internal/tailer/tailer_test.go`; rotação real com `nginx -s reopen` |
| ✅ Parser | valida campos e tipos; inválidos não derrubam o serviço | `parse_test.go` + fuzzing (4,2 M execuções sem falha) |
| ✅ Pipeline goroutines/channels | channels limitados, pool fixo, sem races/deadlocks, contagem exatamente uma vez | `pipeline_test.go` sob `-race` |
| ✅ Sobrecarga definida | backpressure; filas nunca excedem a capacidade | `TestBackpressureBoundsQueues` |
| ✅ Métricas acumuladas e janela de 60 s | totais, classes, códigos, taxas, médias, tops, bytes, histórico | `store_test.go` com relógio falso |
| ✅ Limites de memória | cardinalidade, expiração, truncamento | `TestCardinalityLimit`; 1 M linhas → 8,7 MiB |
| ✅ API REST | 6 rotas; JSON válido vazio; timeouts; validação de `limit` | `api_test.go` |

## Fase 3 — Aplicação demonstrável ✅

| Etapa | Critério de aceitação | Verificação |
|---|---|---|
| ✅ Dashboard | KPIs, histórico, status, tops, alertas, pipeline; atualização 1 s; responsivo; claro/escuro | inspeção no navegador; `TestDashboardServed` |
| ✅ Gerador de tráfego | URL, total, concorrência, distribuição, taxa; cenários; só destinos locais | `cmd/loadgen/main_test.go` |
| ✅ Alertas A e B | campos completos; cooldown; sem bloqueio | `engine_test.go`; integração |
| ✅ Integração com Compose | 18 verificações ponta a ponta | `scripts/integration-test.sh` → 18 OK |

## Fase 4 — Qualidade e apresentação ✅

| Etapa | Critério de aceitação | Verificação |
|---|---|---|
| ✅ Testes de concorrência e desligamento | `go test -race -count=3 ./...` verde; sem vazamento de goroutines | `app_test.go` |
| ✅ Benchmark Go × Python | saídas idênticas; metodologia, hardware e versões registrados | `bench/run.sh` → `bench/results/RESULTS.md` |
| ✅ Documentação | README, ARCHITECTURE, GO_VS_OTHER_LANGUAGES, PRESENTATION, ROADMAP, LICENSES | este repositório |

## Pendências conhecidas ⏳

- ⏳ Executar o teste de integração em um host **Linux nativo** (validado apenas
  em Docker Desktop/WSL2; nada no projeto é específico de Windows).
- ⏳ Rodar os testes com uma toolchain Go instalada no host (aqui foram
  executados no container `golang:1.25-bookworm`, que é o mesmo compilador).

## Melhorias opcionais 💡

- 💡 Endpoint `/metrics` no formato Prometheus/OpenMetrics.
- 💡 Percentis de latência (p50/p95/p99) com histogramas de buckets fixos.
- 💡 Server-Sent Events para o dashboard em vez de polling.
- 💡 Regras estatísticas (média móvel + desvio padrão) além de limites fixos.
- 💡 Métricas por *upstream* quando o NGINX atuar como proxy (`$upstream_response_time`).
- 💡 Persistência opcional de snapshots para sobreviver a reinícios.
- 💡 Manifests Kubernetes (Pod com sidecar e `emptyDir`).
- 💡 Mascaramento por pseudônimo (HMAC com chave por processo).
