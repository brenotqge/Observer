# Resultados do benchmark Go × Python

> Gerado automaticamente por `bench/run.sh` em 2026-11-10T22:11:19Z. Valores são
> **medianas de 5 execuções** sobre o mesmo dataset de 1,000,000 linhas
> (~1% inválidas). Não generalize: veja a metodologia e as ressalvas em
> `GO_VS_OTHER_LANGUAGES.md`.

## Parsing + agregação

| Implementação | Versão | Tempo (s) | Eventos/s | CPU user+sys (s) | Pico RSS (MiB) | Speedup vs Python |
|---|---|---:|---:|---:|---:|---:|
| Go (1 goroutine) | go1.25.14 | 2.308 | 433,358 | 2.35 | 7.7 | 2.9× |
| Go (4 goroutines) | go1.25.14 | 0.688 | 1,453,607 | 2.89 | 14.1 | 9.8× |
| Python (1 thread) | 3.13.18 | 6.761 | 147,917 | 6.76 | 11.8 | 1.0× |

Verificação: as três variantes produziram saída **idêntica byte a byte**
(SHA-256 `1be207e8f6c8e048…`).

## Inicialização do processo (média de 30 execuções, sem trabalho)

| Processo | Tempo por execução |
|---|---:|
| Binário Go (`benchgo -noop`) | 15.3 ms |
| `python3 -c pass` (interpretador vazio) | 15.3 ms |
| `python3 aggregate.py --noop` (com imports) | 50.1 ms |

## Tamanho das imagens Docker (descomprimidas, `docker image inspect`)

| Imagem | Tamanho |
|---|---:|
| `gonginx-observer/observer:dev` (Go, distroless/static) | 15.9 MB |
| `gonginx-bench/python-equivalent:dev` (python:3.13-slim + script) | 176.2 MB |
| `golang:1.25-bookworm` (apenas estágio de build, não é distribuída) | 1190.5 MB |
| `gcr.io/distroless/static-debian12:nonroot` (base) | 6.2 MB |

## Ambiente

- CPU do host: AMD Ryzen 5 5500 (6 núcleos / 12 threads), RAM 24 GB, Windows 11 Pro + Docker Desktop (WSL2)
- CPU vista no container: CPU(s):                               12; Model name:                           AMD Ryzen 5 5500
- Docker 29.9.3 — Docker Desktop / kernel 6.6.87.2-microsoft-standard-WSL2
- CPUs/memória da VM Docker: 12 CPUs, 11.6 GiB
- Go: go version go1.25.14 linux/amd64 · Python: Python 3.13.18
- Ambas as linguagens executadas no mesmo container (`python:3.13-slim`); o
  binário Go é estático e foi montado no container.
