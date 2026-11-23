# Auditoria do Repositório e Contribuições da Equipe

## Objetivo deste documento

Este arquivo registra as tarefas realizadas pelos membros da equipe com base no histórico Git do repositório. Ele também apresenta uma auditoria geral para confirmar se a implementação, os commits e a documentação estão coerentes entre si.

**Origem da análise:** histórico local e remoto `origin/main`, até o commit `07e6420` (2026-11-22).

## Resumo geral

| Membro identificado no Git | E-mail | Commits | Responsabilidades |
|---|---|---:|---|
| brenotqge / Breno Temoteo De Queiroz Guimaraes Elias | `brenotqgelias@gmail.com` | 2 | Criação da base completa do projeto, infraestrutura, observabilidade, testes e documentação inicial |
| davilb64 / Davi Lopes Brito| `davilopesbrito64@gmail.com` / `162513480+davilb64@users.noreply.github.com` | 1 conteúdo + 1 merge | Complementação de Dashboards, sites paralelos, configuração de múltiplos servidores e atualização da documentação |

## Linha do tempo

### 10/11/2026 — Breno — commit `66adaaf`

**Título:** `Add GoNginx Observer: Go sidecar for NGINX Open Source observability`

Foi criado o núcleo do projeto, composto por:

- Serviço em Go para leitura e processamento dos logs do NGINX;
- API HTTP e painel web inicial;
- Armazenamento e agregação de métricas;
- Pipeline de processamento e detecção de alertas;
- Parser de logs e mascaramento de IP;
- Testes unitários das principais camadas internas;
- Configuração Docker e Docker Compose;
- Configuração do NGINX;
- Gerador de carga e ferramentas de benchmark;
- Scripts de integração e documentação técnica;
- Arquivo de dados de exemplo para testes.

Principais áreas:

- `cmd/`
- `internal/`
- `nginx/`
- `bench/`
- `scripts/`
- `README.md`, `ARCHITECTURE.md`, `PRESENTATION.md`, `ROADMAP.md` e demais documentos

### 10/11/2026 — Breno — commit `7a97972`

**Título:** `Keep LF line endings and ignore benchmark temp file`

- Padronizou a terminação de linha como LF por meio de `.gitattributes`;
- Atualizou `.gitignore`;
- Removeu o arquivo temporário `bench/results/err.tmp` do controle de versão.

### 22/11/2026 — Davi — commit `9ece1c2`

**Título:** `feat: adicionar sites paralelos, testes contínuos e dashboards interativos`

Foram adicionados ou modificados:

- Quatro páginas/sites de demonstração no NGINX (`site2`, `site3` e `site4`);
- CSS compartilhado;
- Dashboard interativo no frontend;
- Melhorias na API web e na apresentação das métricas;
- Alterações no parser e nas métricas internas;
- Configuração de múltiplos hosts no NGINX;
- Ajustes no Docker Compose;
- Atualização da arquitetura, apresentação, README e roadmap.

### 22/11/2026 — Davi — merge `07e6420`

- Integração do pull request `#1` da linha `davilb64/main` na branch `main`;
- O commit não contém uma implementação separada, pois apenas integra o commit `9ece1c2`.

## Contribuições por integrante

### brenotqge

Responsabilidades realizadas:

1. Criação da arquitetura inicial da aplicação.
2. Implementação do sidecar Go e do executável principal.
3. Implementação do pipeline de leitura e parsing dos logs.
4. Implementação das métricas, alertas e mascaramento de IP.
5. Desenvolvimento da API e da interface web inicial.
6. Desenvolvimento dos componentes de testes e benchmarks.
7. Configuração do Docker, Docker Compose e NGINX.
8. Criação da documentação inicial do projeto.
9. Padronização de arquivos e configuração do repositório.

### davilb64 / Davi

Responsabilidades realizadas:

1. Ampliação do ambiente com vários sites paralelos.
2. Desenvolvimento dos recursos visuais compartilhados entre os sites.
3. Aperfeiçoamento do dashboard interativo.
4. Aprimoramento da configuração de rotas e acesso do NGINX.
5. Atualização das métricas e dos testes relacionados.
6. Revisão e atualização dos documentos técnicos e da apresentação.


