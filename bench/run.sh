#!/usr/bin/env bash
# Benchmark Go × Python — parsing + agregação do mesmo dataset.
#
# Metodologia (detalhes em GO_VS_OTHER_LANGUAGES.md):
#   1. Compila cmd/benchgo (binário estático) no container golang.
#   2. Gera um dataset determinístico (semente fixa) de N linhas.
#   3. Executa Go (1 goroutine), Go (W goroutines) e Python DENTRO DO MESMO
#      container python:<ver>-slim, alternando as execuções, R repetições.
#   4. Verifica que as saídas são idênticas byte a byte.
#   5. Mede inicialização (processo que sai imediatamente) e tamanho de imagens.
#   6. Gera bench/results/RESULTS.md com medianas e ambiente.
#
# Uso: ./bench/run.sh            (N=1000000, R=5, W=4)
#      N=200000 R=3 ./bench/run.sh
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
N="${N:-1000000}"; R="${R:-5}"; W="${W:-4}"
PY_IMAGE="${PY_IMAGE:-python:3.13-slim}"
ROOT="$(pwd)"
mkdir -p bench/bin bench/data bench/results

echo "==> Compilando benchgo (estático, linux/amd64)"
./scripts/go-docker.sh sh -c 'CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bench/bin/benchgo ./cmd/benchgo && go version' | tee bench/results/go-version.txt

if [ ! -s "bench/data/dataset-$N.jsonl" ]; then
  echo "==> Gerando dataset com $N linhas"
  docker run --rm -v "$ROOT/bench":/bench "$PY_IMAGE" /bench/bin/benchgo -generate "$N" -out "/bench/data/dataset-$N.jsonl"
fi

echo "==> Executando no container $PY_IMAGE ($R repetições)"
docker run --rm -e N="$N" -e R="$R" -e W="$W" -v "$ROOT/bench":/bench -w /bench "$PY_IMAGE" bash /bench/inner.sh

echo "==> Tamanho das imagens"
docker build -q -t gonginx-bench/python-equivalent:dev -f bench/python/Dockerfile bench/python >/dev/null
{
  for img in gonginx-observer/observer:dev gonginx-bench/python-equivalent:dev "$PY_IMAGE" golang:1.25-bookworm gcr.io/distroless/static-debian12:nonroot; do
    size=$(docker image inspect -f '{{.Size}}' "$img" 2>/dev/null || echo 0)
    echo "$img $size"
  done
} > bench/results/images.txt
cat bench/results/images.txt

echo "==> Ambiente"
{
  echo "date $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "docker $(docker version --format '{{.Server.Version}}')"
  echo "docker_os $(docker info --format '{{.OperatingSystem}} / kernel {{.KernelVersion}}')"
  echo "docker_cpus $(docker info --format '{{.NCPU}}')"
  echo "docker_mem_bytes $(docker info --format '{{.MemTotal}}')"
} > bench/results/env.txt
cat bench/results/env.txt

docker run --rm -v "$ROOT/bench":/bench -w /bench "$PY_IMAGE" python3 /bench/summarize.py "$N" "$R" "$W" "${HOST_CPU:-não informado}"
echo "==> Relatório: bench/results/RESULTS.md"
