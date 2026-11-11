#!/usr/bin/env python3
"""Consolida bench/results/*.txt e runs.jsonl em RESULTS.md (medianas)."""
import json
import statistics
import sys

N, R, W, HOST_CPU = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
OUT = "/bench/results"


def kv(path):
    d = {}
    with open(f"{OUT}/{path}") as f:
        for line in f:
            if line.strip():
                k, _, v = line.strip().partition(" ")
                d[k] = v
    return d


runs = [json.loads(l) for l in open(f"{OUT}/runs.jsonl") if l.strip()]
groups = {}
for r in runs:
    label = "Go (1 goroutine)" if r["lang"] == "go" and r["workers"] == 1 else \
        f"Go ({r['workers']} goroutines)" if r["lang"] == "go" else "Python (1 thread)"
    groups.setdefault(label, []).append(r)

med = lambda rs, k: statistics.median(x[k] for x in rs)
py = groups.get("Python (1 thread)")
lines = []
lines.append("| Implementação | Versão | Tempo (s) | Eventos/s | CPU user+sys (s) | Pico RSS (MiB) | Speedup vs Python |")
lines.append("|---|---|---:|---:|---:|---:|---:|")
for label, rs in groups.items():
    t = med(rs, "elapsed_s")
    speed = (med(py, "elapsed_s") / t) if py else float("nan")
    cpu = statistics.median(x["cpu_user_s"] + x["cpu_sys_s"] for x in rs)
    lines.append(f"| {label} | {rs[0]['version']} | {t:.3f} | {med(rs, 'events_per_s'):,.0f} | {cpu:.2f} | "
                 f"{med(rs, 'max_rss_kb') / 1024:.1f} | {speed:.1f}× |")

st = kv("startup.txt")
img = kv("images.txt")
env = kv("env.txt")
cpu = open(f"{OUT}/cpu.txt").read().strip().replace("\n", "; ")
checks = open(f"{OUT}/checksums.txt").read().split()[0]
mb = lambda b: f"{int(b) / 1e6:.1f} MB"

md = f"""# Resultados do benchmark Go × Python

> Gerado automaticamente por `bench/run.sh` em {env.get('date')}. Valores são
> **medianas de {R} execuções** sobre o mesmo dataset de {int(N):,} linhas
> (~1% inválidas). Não generalize: veja a metodologia e as ressalvas em
> `GO_VS_OTHER_LANGUAGES.md`.

## Parsing + agregação

{chr(10).join(lines)}

Verificação: as três variantes produziram saída **idêntica byte a byte**
(SHA-256 `{checks[:16]}…`).

## Inicialização do processo (média de 30 execuções, sem trabalho)

| Processo | Tempo por execução |
|---|---:|
| Binário Go (`benchgo -noop`) | {int(st['go_us']) / 1000:.1f} ms |
| `python3 -c pass` (interpretador vazio) | {int(st['python_bare_us']) / 1000:.1f} ms |
| `python3 aggregate.py --noop` (com imports) | {int(st['python_us']) / 1000:.1f} ms |

## Tamanho das imagens Docker (descomprimidas, `docker image inspect`)

| Imagem | Tamanho |
|---|---:|
| `gonginx-observer/observer:dev` (Go, distroless/static) | {mb(img['gonginx-observer/observer:dev'])} |
| `gonginx-bench/python-equivalent:dev` (python:3.13-slim + script) | {mb(img['gonginx-bench/python-equivalent:dev'])} |
| `golang:1.25-bookworm` (apenas estágio de build, não é distribuída) | {mb(img['golang:1.25-bookworm'])} |
| `gcr.io/distroless/static-debian12:nonroot` (base) | {mb(img['gcr.io/distroless/static-debian12:nonroot'])} |

## Ambiente

- CPU do host: {HOST_CPU}
- CPU vista no container: {cpu}
- Docker {env.get('docker')} — {env.get('docker_os')}
- CPUs/memória da VM Docker: {env.get('docker_cpus')} CPUs, {int(env.get('docker_mem_bytes', 0)) / 2**30:.1f} GiB
- Go: {open(f"{OUT}/go-version.txt").read().strip()} · Python: {open(f"{OUT}/python-version.txt").read().strip()}
- Ambas as linguagens executadas no mesmo container (`python:3.13-slim`); o
  binário Go é estático e foi montado no container.
"""
open(f"{OUT}/RESULTS.md", "w").write(md)
print(md)
