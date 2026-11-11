# Go comparado a Python, Node.js e Java

Este documento separa explicitamente:

- **[Empírico]** — medido neste projeto, neste hardware (seção 3).
- **[Característica]** — propriedade conhecida da linguagem/runtime, não medida aqui.

Nenhuma linguagem "vence" em todos os cenários. A escolha de Go para este
projeto é justificada pelo **perfil do problema**: um serviço pequeno, de longa
duração, com I/O contínuo, concorrência simples, distribuído como container.

## 1. Comparação técnica

| Aspecto | Go | Python (CPython) | Node.js | Java (JVM) |
|---|---|---|---|---|
| **Modelo de concorrência** | goroutines (M:N, pilhas pequenas que crescem) + channels (CSP); bloqueio "parece síncrono" | threads limitadas pelo GIL para CPU; `asyncio` para I/O; `multiprocessing` para paralelismo | event loop único; `async/await`; `worker_threads` para CPU | threads do SO; *virtual threads* desde o Java 21; `java.util.concurrent` muito maduro |
| **Paralelismo de CPU num processo** | sim, nativo (`GOMAXPROCS` = núcleos) | não para bytecode Python (GIL)¹ | não no loop principal; sim com workers | sim |
| **I/O** | bloqueante no código, multiplexado pelo runtime (netpoller) | síncrono ou `asyncio` (*function coloring*) | assíncrono por padrão (libuv) | bloqueante, NIO ou virtual threads |
| **Memória** | baixa; GC concorrente sem gerações; sem VM | moderada; objetos com overhead alto; contagem de referências + GC | moderada/alta (V8 + heap JS) | alta por padrão (heap da JVM, JIT); ajustável |
| **Inicialização** | binário nativo, ms | interpretador + imports (dezenas a centenas de ms) | dezenas de ms | centenas de ms a segundos (JIT); GraalVM native-image reduz |
| **Artefato** | um binário estático, sem dependências de runtime | código + interpretador + pacotes (`venv`) | código + `node_modules` + runtime | JAR + JVM (ou imagem nativa) |
| **Imagem Docker típica** | `scratch`/`distroless` (poucos MB) | `python:slim` (~100–180 MB) | `node:slim` (~200 MB) | JRE slim (~200+ MB) |
| **Deploy** | copiar o binário; cross-compile trivial (`GOOS/GOARCH`) | gerenciar versão do interpretador e dependências nativas | gerenciar runtime e dependências | gerenciar JVM, flags de memória |
| **Tipagem** | estática, simples, inferência local | dinâmica (type hints opcionais) | dinâmica (TypeScript opcional) | estática, rica (generics, hierarquias) |
| **Biblioteca padrão para este projeto** | HTTP, JSON, embed, slog, testes, race detector, fuzzing — tudo incluso | HTTP básico, JSON; servidor de produção requer libs externas | HTTP, JSON; testes nativos recentes | HTTP (HttpServer básico), JSON requer libs |
| **Ferramentas** | `go fmt`, `go vet`, `go test -race`, profiler, fuzzing nativos | pytest, mypy, ruff (externos) | eslint, jest (externos) | JUnit, Maven/Gradle (externos), excelente profiling |
| **Ecossistema de dados/ML** | limitado | excelente (NumPy, pandas) | moderado | bom (Spark, etc.) |

¹ O Python 3.13 tem uma build experimental *free-threaded* (sem GIL), ainda não padrão.

### Custo operacional e manutenção

- **[Característica]** Em Go, um serviço como este cabe em um binário único com
  a biblioteca padrão — sem cadeia de dependências para auditar e atualizar.
  O `go.mod` deste projeto não tem nenhum `require`.
- **[Característica]** `go fmt` elimina discussões de estilo; a linguagem é
  pequena, o que facilita a leitura de código alheio.
- **[Empírico]** Footprint em execução (seção 3): imagem de 15,9 MB e
  ~7–9 MiB de RAM; isso permite rodar como sidecar em cada Pod com custo marginal.
- **[Característica]** Python e Node.js são mais rápidos para prototipar e têm
  ecossistemas maiores; Java tem ferramentas corporativas e JIT que, em
  processos longos e CPU-intensivos, pode igualar ou superar Go.

## 2. Limitações e trade-offs de Go

- **`encoding/json` é baseado em reflexão** e relativamente lento: no nosso
  benchmark, o Go sequencial foi "só" 2,9× mais rápido que o Python, cujo
  módulo `json` é escrito em C. Bibliotecas de terceiros (ou o futuro
  `encoding/json/v2`) seriam mais rápidas, ao custo de dependências.
- **Garbage collector**: pausas curtas, mas existem; não é adequado para tempo
  real rígido (C/C++/Rust são).
- **Expressividade**: sem enums algébricos, sem exceções (tratamento explícito
  de erros é verboso), generics mais limitados que Java/Rust.
- **Goroutines não são gratuitas**: vazamentos (goroutines bloqueadas para
  sempre) são um erro comum; exigem disciplina com `context` e fechamento de
  channels — por isso este projeto testa o desligamento.
- **Data races são possíveis** (diferente de Rust): o race detector ajuda,
  mas só encontra corridas que acontecem durante os testes.
- **Ecossistema** menor em ciência de dados, ML e GUI.
- **Binários** maiores que os de C (runtime embutido) — irrelevante aqui.

## 3. Benchmark empírico

### Metodologia

- **Tarefa**: ler um arquivo JSON Lines no formato do NGINX, validar cada linha
  (JSON, presença/tipo dos campos, faixas, IP válido), remover query string e
  agregar: total, inválidas, bytes, soma de `request_time` em ms, contagem por
  classe e código, IPs distintos, top-10 endpoints e top-10 IPs.
- **Implementações**: [`cmd/benchgo/main.go`](cmd/benchgo/main.go) e
  [`bench/python/aggregate.py`](bench/python/aggregate.py), ambas só com a
  biblioteca padrão. Regras idênticas, inclusive arredondamento
  (`int(x*1000+0.5)`, com conversão explícita no Go para impedir FMA).
- **Verificação**: as saídas JSON das três variantes são comparadas por
  SHA-256 e são **idênticas byte a byte**.
- **Dataset**: 1.000.000 linhas (183 MB), geradas deterministicamente
  (semente fixa), ~1% inválidas, 5.000 IPs, 200 caminhos, distribuição não uniforme.
- **Ambiente**: as duas linguagens rodam **no mesmo container**
  (`python:3.13-slim`; o binário Go estático é montado nele). O dataset é
  copiado para o filesystem do container (bind mounts do Docker Desktop têm
  I/O lento) e lido uma vez antes para aquecer o cache de páginas.
- **Execuções**: 5 rodadas alternando as variantes; reportamos a **mediana**.
- **Métricas**: tempo de parede medido dentro do processo (excluindo
  inicialização), eventos/s, CPU user+sys e pico de RSS via `getrusage`.
- **Reprodução**: `./bench/run.sh` (parâmetros `N`, `R`, `W`).

### Hardware e versões

- AMD Ryzen 5 5500 (6 núcleos / 12 threads), 24 GB RAM, Windows 11 Pro.
- Docker Desktop 29.9.3, VM WSL2 (kernel 6.6.87.2) com 12 CPUs e 11,6 GiB.
- Go 1.25.14 (linux/amd64) · Python 3.13.18.

### Resultados (medianas de 5 execuções, 1 M de linhas)

| Implementação | Tempo (s) | Eventos/s | CPU user+sys (s) | Pico RSS (MiB) | Speedup vs Python |
|---|---:|---:|---:|---:|---:|
| Go — 1 goroutine | 2,308 | 433.358 | 2,35 | 7,7 | 2,9× |
| Go — 4 goroutines (channels + lotes) | 0,688 | 1.453.607 | 2,89 | 14,1 | 9,8× |
| Python — 1 thread | 6,761 | 147.917 | 6,76 | 11,8 | 1,0× |

| Inicialização (processo vazio, média de 30) | Tempo |
|---|---:|
| binário Go | 15,3 ms |
| `python3 -c pass` | 15,3 ms |
| `python3 aggregate.py --noop` (com imports) | 50,1 ms |

| Imagem (descomprimida) | Tamanho |
|---|---:|
| Observer em Go (`distroless/static`) | 15,9 MB |
| Equivalente mínimo em Python (`python:3.13-slim` + script) | 176,2 MB |

Relatório completo gerado: [`bench/results/RESULTS.md`](bench/results/RESULTS.md).

### Medições do Observer em execução (Docker, mesmo host)

| Situação | Medida |
|---|---|
| RAM em repouso após o teste de integração (`docker stats`) | 7,3 MiB |
| 1 M de linhas anexadas ao log ao vivo, até `lines_read = 1.000.000` | ~1,7 s (≈ 575 mil linhas/s, 4 workers) |
| RAM após ingerir 1 M de linhas com 5.000 IPs | 8,7 MiB (limite do container: 64 MiB) |
| Microbenchmark `logparse.Parse` | ~2,0 µs/linha, 15 alocações |
| Microbenchmark `Store.Add` | ~190 ns/evento, 2 alocações |

### Interpretação

- **Throughput sequencial**: Go processou ~2,9× mais eventos/s com um núcleo.
  A diferença é menor do que muitos esperariam porque o parsing JSON do
  Python é implementado em C; o custo do Python está no laço e nos objetos.
- **Paralelismo**: com 4 goroutines, Go chegou a 9,8× usando ~1,2× mais CPU
  total — o paralelismo real em um único processo é a vantagem estrutural
  mais clara. Em CPython (com GIL) o equivalente exigiria `multiprocessing`,
  com serialização entre processos e maior uso de memória (não implementado
  aqui, portanto não medido).
- **Memória**: o Go sequencial teve o menor pico (7,7 MiB), mas o **Python
  (11,8 MiB) usou menos memória que o Go paralelo (14,1 MiB)**, que mantém
  lotes de linhas em trânsito e agregados parciais por worker. Memória é um
  trade-off de projeto, não uma vitória automática.
- **Inicialização**: um processo vazio custou o mesmo nas duas linguagens
  (~15 ms, dominado pela criação de processo no WSL2). A diferença aparece
  quando o Python importa módulos (50 ms). Para um serviço de longa duração,
  isso é irrelevante; para CLIs e funções serverless, conta.
- **Imagem**: 11× menor em Go, por dispensar o interpretador. Isso reduz
  tempo de pull, armazenamento e superfície de ataque.

### Ressalvas

- Um único hardware, sob virtualização (WSL2); números absolutos variam.
- Benchmark de um tipo de carga (parsing JSON + mapas). Não representa
  aplicações numéricas (onde Python usa NumPy) nem servidores de I/O puro.
- Node.js e Java **não foram medidos**; as afirmações sobre eles são
  características conhecidas, não resultados deste projeto.
- Não foram usadas otimizações específicas (PyPy, orjson, bibliotecas JSON
  rápidas em Go, PGO). Elas mudariam os números.

## 4. Conclusão

Para **este** problema — um sidecar de observabilidade que roda continuamente
ao lado do NGINX — Go oferece uma combinação favorável: concorrência simples e
paralela no mesmo processo, consumo de memória baixo e previsível, binário
único sem dependências e imagem mínima. Python seria mais rápido de prototipar
e excelente para análises offline dos mesmos logs; Node.js atenderia bem a
parte de I/O e dashboard; Java ofereceria desempenho comparável em regime
estável, com mais memória e tempo de inicialização. As medições confirmam
vantagens concretas, mas menores e mais nuançadas do que comparações
genéricas costumam sugerir.
