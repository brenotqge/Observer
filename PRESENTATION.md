# Roteiro de apresentação — GoNginx Observer

Duração sugerida: **15–20 minutos** (12 de fala + 5–8 de demonstração).

## Antes da aula (checklist)

```bash
docker compose build
```

```bash
docker compose --profile tools build loadgen
```

```bash
docker compose up -d
```

- Abrir http://localhost:8080 (dashboard) e http://localhost:8081 (site).
- Deixar um terminal com fonte grande, já na pasta do projeto.
- Plano B sem internet: as imagens ficam em cache local após o build.
- Plano C: capturas de tela do dashboard e o relatório `bench/results/RESULTS.md`.

## 1. Abertura (1 min)

"Como saber, **agora**, quantas requisições meu servidor web está recebendo,
quantas estão falhando e se algum cliente está fazendo algo fora do normal?"

## 2. O que é o NGINX (2 min)

- Servidor HTTP, *reverse proxy* e balanceador; um dos servidores web mais usados.
- Arquitetura orientada a eventos (poucos processos, milhares de conexões).
- Duas edições: **Open Source** (licença BSD-2) e **NGINX Plus** (comercial),
  que inclui dashboard em tempo real e API de métricas.

## 3. Problema identificado (1,5 min)

- O NGINX Open Source escreve logs, mas não oferece painel, rankings ou alertas.
- `stub_status` só mostra contadores de conexão.
- Objetivo: criar essa camada **sem modificar o NGINX** e **sem usar o Plus**.

## 4. Solução desenvolvida (3 min)

Mostrar o diagrama do [README](README.md#arquitetura).

- NGINX escreve **JSON Lines** em um volume compartilhado.
- Observer (Go) monta o volume **somente leitura** — padrão **sidecar**.
- Pipeline: Tailer → channel → pool de parsers → channel → agregador.
- API REST + dashboard embutidos no mesmo binário.
- Dois serviços no Docker Compose, um comando para subir tudo.

## 5. Por que Go (3 min)

Abrir `internal/pipeline/pipeline.go` e mostrar ~20 linhas:

- **goroutines**: `workers.Go(p.parseWorker)` — um pool fixo, não ilimitado.
- **channels com capacidade limitada**: `make(chan []byte, 1024)`.
- **quem envia fecha**: `defer close(p.raw)`; `workers.Wait(); close(p.events)`.
- **`context.Context`**: o mesmo sinal desliga todas as goroutines.
- **Backpressure**: fila cheia → o leitor espera; o arquivo é o buffer.

Números do projeto (ver [GO_VS_OTHER_LANGUAGES.md](GO_VS_OTHER_LANGUAGES.md)):

- Imagem do Observer: **15,9 MB** contra **176 MB** de um equivalente Python.
- Memória em execução: **~7–9 MiB**.
- Benchmark de parsing: Go **2,9×** (1 núcleo) e **9,8×** (4 goroutines) mais
  rápido que Python — e onde o Python foi melhor (memória vs. Go paralelo).

## 6. Demonstração prática (6–8 min)

1. **Dashboard vazio** — http://localhost:8080. "A API responde mesmo sem dados."
2. **Tráfego normal** (mostra req/s, status, endpoints, IPs, histórico):

   ```bash
   docker compose run --rm loadgen -scenario normal -n 3000 -c 10 -rate 100
   ```

3. **Ver o log bruto** que o NGINX está escrevendo:

   ```bash
   docker compose exec nginx tail -n 3 /var/log/observer/access.jsonl
   ```

4. **Regra A — pico de um IP** (alerta "Volume elevado por IP"):

   ```bash
   docker compose run --rm loadgen -scenario spike -n 400
   ```

5. **Regra B — onda de erros 500** (alerta "Taxa de 5xx elevada"):

   ```bash
   docker compose run --rm loadgen -scenario errors -n 1500
   ```

6. **API diretamente**:

   ```bash
   curl -s http://localhost:8080/api/v1/alerts
   ```

7. **Concorrência visível**: no card "Pipeline Go", mostrar goroutines,
   filas (len/cap), linhas lidas = eventos agregados.
8. **Desligamento gracioso**:

   ```bash
   docker compose stop observer
   ```

   ```bash
   docker compose logs observer --tail 5
   ```

   Mostrar "pipeline encerrado", "desligamento concluído", código 0.

Enfatizar: "alertas são **indicadores heurísticos** — um pico pode ser um
crawler legítimo; por isso não bloqueamos nada automaticamente."

## 7. Qualidade (1 min)

- Testes de unidade, concorrência (`-race`), fuzzing do parser, HTTP,
  desligamento e um log **real** do NGINX.
- Teste de integração com Compose: `./scripts/integration-test.sh` (18 verificações).

## 8. Aplicações futuras (1,5 min)

- Exportar métricas para Prometheus/Grafana; alertas via Alertmanager/webhooks.
- Rodar como sidecar em Kubernetes (`emptyDir` compartilhado).
- Percentis de latência (p95/p99), métricas por *upstream*, detecção estatística
  (média móvel/desvio), geolocalização com bases locais.
- Agregação central de várias instâncias (fila + agregador).

## 9. Custos operacionais (1 min)

- Um sidecar por servidor: ~16 MB de imagem, < 10 MiB de RAM, CPU ≈ 0 em repouso.
- Zero dependências externas → sem atualizações de bibliotecas de terceiros.
- Sem banco de dados → nada para provisionar, mas também sem histórico.

## 10. Limitações e conclusões (1,5 min)

- Métricas em memória (reinício zera); uma única instância; latência ≈ 250 ms.
- Sem autenticação (portas apenas em localhost).
- Go não é universalmente superior: o JSON do Python é em C, o Python usou
  menos memória que o Go paralelo, e a inicialização vazia empatou.
- **Conclusão**: para um serviço de infraestrutura pequeno, concorrente e de
  longa duração, Go entregou simplicidade de deploy, baixo consumo e
  concorrência fácil de explicar — exatamente o que o problema pedia.

## Perguntas prováveis

- *Por que não ler os logs pelo stdout do container?* Exigiria acesso à API do
  Docker (socket montado = risco de segurança) e misturaria logs.
- *E se o Observer cair?* O NGINX não é afetado; as métricas recomeçam.
- *Perde eventos sob carga?* Não: backpressure, o arquivo segura a fila.
- *Por que não fsnotify?* Só biblioteca padrão; eventos de FS são pouco
  confiáveis em volumes do Docker Desktop. Polling de 250 ms basta.
