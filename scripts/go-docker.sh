#!/usr/bin/env sh
# Executa a toolchain Go dentro do container oficial golang (útil quando o Go
# não está instalado no host). Exemplos:
#   ./scripts/go-docker.sh go test ./...
#   ./scripts/go-docker.sh go test -race ./...
#   ./scripts/go-docker.sh go run ./cmd/loadgen -url http://host.docker.internal:8081
# A imagem Debian (bookworm) inclui gcc, necessário para o race detector (CGO).
set -eu
GO_IMAGE="${GO_IMAGE:-golang:1.25-bookworm}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Git Bash no Windows converte caminhos POSIX; desativamos a conversão.
export MSYS_NO_PATHCONV=1
exec docker run --rm \
  -v "$ROOT":/src -w /src \
  -v gonginx-gomod:/go/pkg/mod -v gonginx-gocache:/root/.cache/go-build \
  -e GOFLAGS="${GOFLAGS:-}" \
  "$GO_IMAGE" "$@"
