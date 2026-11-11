# Arquitetura — GoNginx Observer

## 1. Visão geral

O sistema tem dois serviços permanentes e um auxiliar:

| Serviço    | Imagem                         | Papel |
|------------|--------------------------------|-------|
| `nginx`    | `nginx:1.28-alpine` + config   | servidor web; escreve `access.jsonl` no volume `nginx-logs` |
| `observer` | Go estático em `distroless/static` (15,9 MB) | lê o log (volume **somente leitura**), agrega, alerta, serve API/dashboard |
| `loadgen`  | Go estático (profile `tools`)  | gerador de tráfego sob demanda; não fica em execução |

```
                        HTTP (127.0.0.1:8081)
 Clientes / loadgen ─────────────────────────▶ ┌────────────────────┐
   (X-Forwarded-For com IPs TEST-NET)          │  NGINX Open Source │
                                               │  log_format JSON   │
                                               └─────────┬──────────┘
                                                         │ append (O_APPEND)
                                                         ▼
                                      volume nginx-logs: access.jsonl
                                                         │ leitura (ro)
 ┌───────────────────────────────── observer ────────────┼─────────────────────────────┐
 │                                                        ▼                             │
 │  ┌──────────┐  chan []byte (1024)  ┌────────────┐  chan Event (1024)  ┌───────────┐  │
 │  │ Tailer   │ ───────────────────▶ │ Parser ×N  │ ──────────────────▶ │ Agregador │  │
 │  │ 1 gorout.│   backpressure       │ N gorout.  │                     │ 1 gorout. │  │
 │  └──────────┘                      └────────────┘                     └─────┬─────┘  │
 │                                                                         Add │         │
 │                                                                             ▼         │
 │  ┌──────────────────┐  WindowSummary/IPCounts  ┌──────────────────────────────────┐  │
 │  │ Motor de alertas │ ◀─────────────────────── │ metrics.Store (RWMutex)          │  │
 │  │ ticker 1 s       │                          │ acumulado + ring buffer 60×1 s   │  │
 │  └────────┬─────────┘                          └───────────────┬──────────────────┘  │
 │           │ Recent()                                 Snapshot()│                      │
 │           ▼                                                    ▼                      │
 │  ┌──────────────────────────────────────────────────────────────────────────────┐    │
 │  │ net/http (goroutine por conexão): /healthz, /api/v1/*, dashboard (go:embed)  │    │
 │  └──────────────────────────────────────────────────────────────────────────────┘    │
 └──────────────────────────────────────────────────────────────────── 127.0.0.1:8080 ──┘
```

## 2. Fluxo dos dados

1. O NGINX escreve uma linha JSON por requisição em `/var/log/observer/access.jsonl`
   (`log_format observer_json escape=json`). O caminho registrado é o
   **original** do cliente, sem query string (via `map $request_uri`), porque
   `$uri` muda após redirecionamentos internos (ex.: `/` → `/index.html`).
2. O **Tailer** faz polling (250 ms) do arquivo, lê blocos de 32 KiB e monta
   linhas completas. Fragmentos sem `\n` ficam em buffer até serem concluídos.
3. Cada linha vai para o channel `raw`. Um **pool fixo de parsers** decodifica
   o JSON, valida campos (presença, tipos, faixas, IP válido) e normaliza
   método e caminho. Linhas inválidas são contadas e descartadas sem derrubar
   o serviço (o conteúdo nunca é logado, por privacidade).
4. Eventos válidos vão para o channel `events`, consumido por **uma única
   goroutine agregadora**, que chama `Store.Add`.
5. O **Store** mantém contadores acumulados e uma janela móvel de 60 buckets
   de 1 s indexados pelo timestamp do evento.
6. O **motor de alertas** avalia as regras a cada segundo sobre a janela.
7. Os **handlers HTTP** leem cópias do estado e respondem JSON; o dashboard
   consulta a API a cada segundo.

## 3. Responsabilidades por pacote

| Pacote              | Responsabilidade | Depende de |
|---------------------|------------------|------------|
| `internal/tailer`   | `tail -F` portátil: linhas completas, limite de tamanho, arquivo ausente, truncamento, rotação | stdlib |
| `internal/logparse` | contrato do formato de log; validação e normalização | stdlib |
| `internal/pipeline` | goroutines, channels, ciclo de vida e backpressure | `logparse` + interfaces `Source`/`Sink` |
| `internal/metrics`  | agregação acumulada e em janela, limites de cardinalidade | `logparse` |
| `internal/alerts`   | regras heurísticas, cooldown, histórico limitado | interface `Source` (métricas) |
| `internal/ipmask`   | mascaramento de IP para exibição | stdlib |
| `internal/api`      | rotas REST, cabeçalhos de segurança, dashboard embutido | todos acima |
| `internal/config`   | variáveis de ambiente com validação agregada | stdlib |
| `internal/app`      | montagem dos componentes e desligamento gracioso | todos |

Interfaces pequenas (`pipeline.Source`, `pipeline.Sink`, `alerts.Source`)
desacoplam os pacotes e permitem testes com implementações falsas.
**Nenhuma dependência externa** é usada: o `go.mod` não tem `require`.

## 4. Uso de goroutines e channels

Cada goroutine tem uma responsabilidade justificável — nenhuma existe só para
"mostrar concorrência":

| Goroutine           | Quantidade | Por que existe |
|---------------------|-----------:|----------------|
| Tailer              | 1 | I/O bloqueante de arquivo, isolado do resto; leitura sequencial não se beneficia de paralelismo |
| Parsers             | N (padrão 4) | parsing JSON é o estágio de **CPU** dominante (~2 µs/linha contra ~0,19 µs da agregação — ver `bench/results/go-microbench.txt`) |
| Agregador           | 1 | **único escritor** do Store: elimina contenção de escrita e torna a contagem trivialmente correta |
| Fechador de `events`| 1 | espera o `WaitGroup` dos parsers e fecha o channel (quem envia, fecha) |
| Motor de alertas    | 1 | avaliação periódica (ticker), desacoplada da taxa de ingestão |
| Servidor HTTP       | 1 + 1 por conexão | modelo padrão do `net/http` |

**Propriedade e fechamento dos channels.** O tailer é o único remetente de
`raw` e o fecha ao terminar; os parsers são os remetentes de `events`, que é
fechado por uma goroutine após `workers.Wait()`. Assim nunca há envio em
channel fechado.

**Desligamento gracioso** (`SIGTERM` do Docker ou `Ctrl+C`):

1. `signal.NotifyContext` cancela o contexto raiz.
2. O servidor HTTP para de aceitar conexões (`Shutdown` com timeout de 5 s).
3. O tailer retorna (inclusive se estiver bloqueado num envio, graças ao
   `select` com `ctx.Done()`), fechando `raw`.
4. Os parsers **drenam** o que já estava em `raw` e terminam; `events` é
   fechado; o agregador drena e termina. Nenhuma linha já lida é perdida ou
   contada duas vezes (testado em `TestShutdownDrainsInFlight`).
5. O motor de alertas retorna no próximo `select`.
6. `app.Run` espera todas as goroutines e o processo sai com código 0
   (verificado no teste de integração: saída em ~1 s).

**Contagem no máximo uma vez.** Cada linha é lida uma única vez do arquivo
(offset monotônico), entregue a exatamente um parser (semântica de channel) e
o evento é agregado uma vez. `TestEachEventCountedExactlyOnce` processa 20.000
linhas com 8 workers e filas de tamanho 4 sob `-race`, verificando cada ID.

**Sobrecarga (backpressure).** Todos os channels têm capacidade limitada e
envios bloqueantes. Se o processamento não acompanhar, as filas enchem e o
tailer **para de ler**; as linhas aguardam no próprio arquivo, que funciona
como buffer durável. Consequências explícitas:

- memória limitada (no máximo `raw_cap + event_cap + workers` itens em trânsito);
- o NGINX **nunca** é desacelerado, pois escreve no arquivo e não no Observer;
- nenhum evento é descartado; a defasagem aparece em `queue_len` na API.

A alternativa (descartar eventos com `select { default: }`) reduziria a
latência sob carga extrema à custa de métricas incorretas; foi rejeitada.

**Estado compartilhado.** O `Store` usa `sync.RWMutex`, com um escritor e
vários leitores; leitores recebem cópias. Contadores do pipeline usam
`sync/atomic`. O motor de alertas tem seu próprio mutex. O race detector é
executado em todos os testes.

## 5. Métricas e memória

- **Acumuladas** (desde a inicialização): total, classes 1xx–5xx, contagem por
  código, taxa de erro (4xx+5xx) e de erro de servidor (5xx), tempo médio,
  bytes, endpoints e IPs.
- **Janela móvel** (60 s, buckets de 1 s): requisições, req/s, erros, tempo
  médio, bytes, IPs distintos e histórico por segundo. Nos primeiros 60 s de
  vida, req/s divide pelo tempo efetivamente observado.
- Eventos com timestamp no futuro são atribuídos ao segundo atual; eventos mais
  antigos que a janela entram só no acumulado (`late_events`).

**Limites de memória:**

| Estrutura                  | Limite |
|----------------------------|--------|
| endpoints / IPs acumulados | `OBSERVER_MAX_TRACKED_KEYS` (1000) chaves; excedentes somados em `(other)` |
| IPs na janela              | mesmo limite; buckets expiram após 60 s |
| códigos HTTP               | naturalmente ≤ 500 (100–599) |
| caminho                    | truncado em 256 bytes |
| linha de log               | 16 KiB (maiores são descartadas) |
| alertas                    | 100 mais recentes |
| channels                   | 1024 + 1024 itens |

Medição real: após ingerir 1 milhão de linhas com 5.000 IPs distintos, o
container do Observer usava **8,7 MiB de RSS** (limite configurado: 64 MiB).

## 6. Regras de alerta

| Regra | Condição | Padrão |
|-------|----------|--------|
| A — `ip_request_rate` | requisições de um IP na janela > limite | 300 em 60 s |
| B — `server_error_rate` | 5xx/total na janela > limite **e** total ≥ mínimo | 20% com ≥ 50 amostras |

Cada alerta contém tipo, momento, entidade, métrica, valor observado, limite,
janela e descrição. Um *cooldown* (60 s) por regra+entidade evita repetição.
Alertas são **indicadores heurísticos** (proxies corporativos, crawlers,
testes de carga e deploys geram falsos positivos) e não disparam ações.

## 7. Decisões arquiteturais

| Decisão | Alternativa considerada | Motivo |
|---------|-------------------------|--------|
| Ler o arquivo de log (sidecar) | módulo NGINX, `syslog` UDP, `stub_status` | não modifica o NGINX; arquivo é durável e funciona como buffer; `stub_status` só traz contadores de conexão |
| JSON Lines com `escape=json` | formato `combined` + regex | parsing robusto e explícito; evolução do formato sem regex frágil |
| Polling (250 ms) | inotify/fsnotify | apenas biblioteca padrão; funciona igual em Linux e volumes do Docker Desktop, onde eventos de FS nem sempre se propagam |
| Estado em memória | banco de dados/TSDB | escopo acadêmico, zero dependências; documentado que reinício zera métricas |
| Agregador único + RWMutex | mapas sharded, `sync.Map`, atomics por chave | agregação custa ~190 ns/evento — longe de ser gargalo; simplicidade |
| Backpressure | descarte de eventos | exatidão das métricas; NGINX não é afetado |
| `distroless/static` | `scratch`, `alpine` | sem shell (menor superfície), mas com `/etc/passwd`, CA e tzdata; usuário não-root 65532 |
| Healthcheck pelo próprio binário (`-healthcheck`) | `curl` na imagem | imagem sem shell nem ferramentas extras |
| IP real via `realip` + XFF (redes privadas) | usar sempre o IP do socket | permite IPs fictícios distintos na demo; em produção restringir ao balanceador |
| `$request_uri` sem query | `$uri` | mantém o caminho pedido pelo cliente e não coleta query strings |

## 8. Segurança

- Volume de logs montado `:ro` no Observer; container com `read_only: true`,
  `cap_drop: ALL`, `no-new-privileges`, usuário não-root e limite de 64 MiB.
- Sem socket Docker montado. Portas publicadas apenas em `127.0.0.1`.
- O arquivo é criado pelo NGINX com modo `0644`; nenhuma permissão foi ampliada.
- Servidor HTTP com `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`,
  `IdleTimeout` e limite de cabeçalhos.
- Dashboard com CSP restritiva (`default-src 'self'`); dados vindos dos logs
  são inseridos com `textContent` (sem XSS por caminhos maliciosos).
- Parser testado com *fuzzing* (`FuzzParse`).

## 9. Trade-offs e limitações

- **Estado volátil**: reiniciar zera métricas e alertas; não há histórico longo.
- **Instância única**: métricas não são compartilhadas entre réplicas.
- **Rotação**: suportada (rename + `nginx -s reopen`, e truncamento/copytruncate).
  Uma linha incompleta no fim do arquivo antigo no momento da troca é descartada.
- **Ordem temporal**: com N parsers a ordem dos eventos não é preservada; as
  métricas não dependem dela (buckets por timestamp).
- **Latência**: até ~250 ms (polling) + tempo de processamento.
- **`request_time`**: para respostas estáticas pequenas o NGINX registra
  `0.000`; por isso a demo tem o endpoint `/slow` (`limit_rate`).
- **Confiança em `X-Forwarded-For`**: na demo confiamos em redes privadas
  inteiras; em produção, apenas no IP do balanceador.
- **Sem autenticação**: aceitável só porque a porta é local.

## 10. Evolução para cloud / Kubernetes

- **Sidecar no mesmo Pod**: NGINX e Observer compartilhando um `emptyDir`
  (o mesmo padrão do volume Docker), com `readOnly: true` no Observer.
  `livenessProbe`/`readinessProbe` em `/healthz`; `resources.limits.memory: 64Mi`.
- **Exportar métricas** no formato Prometheus/OpenMetrics (`/metrics`) e usar
  Prometheus + Grafana para histórico, ou OpenTelemetry para traces/logs.
- **Agregação distribuída**: com várias réplicas, cada sidecar exporta séries e
  a soma é feita no backend de métricas; ou os eventos vão para uma fila
  (Kafka/NATS) e um agregador central.
- **Alertas**: encaminhar para Alertmanager/webhooks (Slack, PagerDuty).
- **Persistência**: TSDB para séries; o Observer continuaria sem estado.
- **Segurança**: autenticação (OIDC) atrás de um ingress, TLS, RBAC.
- **Custo**: o footprint medido (≈16 MB de imagem, < 10 MiB de RAM, CPU
  próxima de zero em repouso) permite um sidecar por Pod com custo marginal.
