# syntax=docker/dockerfile:1
#
# Multi-stage build: compila os binários Go estáticos e copia apenas o
# executável para uma imagem final mínima (distroless/static, sem shell).

ARG GO_VERSION=1.25

# ---- Estágio 1: compilação ---------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src

# go.mod primeiro para aproveitar cache de camadas (projeto sem dependências externas).
COPY go.mod ./
RUN go mod download

COPY . .
ARG VERSION=dev
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/observer ./cmd/observer \
 && go build -trimpath -ldflags="-s -w" -o /out/loadgen ./cmd/loadgen

# ---- Imagem do gerador de tráfego (uso opcional, profile "tools") -------------
FROM gcr.io/distroless/static-debian12:nonroot AS loadgen
COPY --from=build /out/loadgen /loadgen
USER nonroot:nonroot
ENTRYPOINT ["/loadgen"]

# ---- Imagem final do Observer (alvo padrão) ----------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS observer
COPY --from=build /out/observer /observer
# UID/GID 65532, sem privilégios. O log do NGINX é criado com modo 0644,
# portanto a leitura funciona sem permissões amplas.
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/observer", "-healthcheck"]
ENTRYPOINT ["/observer"]
