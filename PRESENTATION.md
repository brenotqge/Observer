# Roteiro de vídeo — GoNginx Observer

Duração máxima: 10 minutos.

## Objetivo do vídeo

Apresentar o problema, a escolha de Go, a arquitetura do projeto, a experiência
dos usuários e uma demonstração prática. O roteiro combina explicação técnica
com exemplos visuais, sem depender de conhecimento prévio.

## 0:00–0:45 — Abertura

Abrir o vídeo já no painel principal do Observer em
`http://localhost:8080`.

Fala sugerida:

"Este projeto é um sistema de observabilidade para NGINX Open Source. Ele
monitora acessos, erros e comportamentos fora do normal em quatro sites
executados em portas diferentes. Hoje vamos ver por que usamos Go, para quem
o projeto foi feito, como ele funciona e como demonstrar o sistema na prática."

## 0:45–1:45 — Por que Go

Mostrar brevemente `go.mod`, `internal/pipeline/pipeline.go` e
`GO_VS_OTHER_LANGUAGES.md`.

Pontos a falar:

- Go foi criado no Google no final dos anos 2000 por Robert Griesemer, Rob Pike
  e Ken Thompson. A primeira versão pública foi lançada em 2009.
- O projeto usa Go 1.25.14. O projeto não usa bibliotecas externas no módulo.
- A linguagem foi escolhida porque o problema exige um processo contínuo,
  leitura concorrente de dados, baixa memória e implantação simples.
- Goroutines permitem executar etapas em paralelo dentro do mesmo processo.
  Channels organizam a comunicação entre os stages do pipeline.
- O resultado também é um binário único. Isso facilita executar o Observer
  como um sidecar junto do NGINX.
- Em benchmark deste projeto, Go com 4 goroutines processou cerca de 1,45 milhão
  de eventos por segundo, contra 147 mil por segundo com Python em uma rodada
  sequencial. O ambiente, o hardware e a metodologia estão em
  `GO_VS_OTHER_LANGUAGES.md`.

Não transformar o vídeo em uma comparação absoluta. Explicar que Python seria
ótimo para análise de dados e prototipagem, Node.js seria adequado para I/O e
Java oferece ferramentas maduras para sistemas maiores. Para este problema
pequeno e contínuo, Go teve a melhor combinação entre concorrência simples,
baixo consumo e implantação.

## 1:45–3:00 — Projeto e domínio

Mostrar o diagrama de `README.md` e depois o arquivo
`nginx/conf.d/default.conf`.

Pontos a falar:

- Premissa: o NGINX já registra acessos em um arquivo JSON Lines.
- Premissa: o projeto não deve modificar o NGINX nem depender da edição comercial.
- Usuários principais: administradores de servidores, equipes técnicas e
  pessoas iniciantes que precisam entender rapidamente se os sites estão
  funcionando.
- Domínio de aplicação: monitoramento em tempo real de servidores web.
- O sistema observa quantidades de acessos, respostas 2xx, 3xx, 4xx e 5xx,
  páginas mais usadas, IPs ativos, latência e alertas.
- Os dados ficam em memória e são reiniciados junto do Observer.
- O acesso é restrito a `localhost`, portanto não exige autenticação nesta
  demonstração acadêmica.

## 3:00–4:00 — Arquitetura

Mostrar uma visão geral dos diretórios e explicar o fluxo em uma frase:

"O NGINX recebe a requisição, escreve uma linha no arquivo de log, o Tailer lê
a linha, os parsers validam em paralelo, o agregador conta os dados e a API
entrega o resultado ao dashboard."

Mostrar estes elementos:

- `nginx/conf.d/default.conf`: quatro portas e formatos de resposta.
- `internal/tailer`: leitura contínua.
- `internal/logparse`: validação.
- `internal/pipeline`: goroutines e channels.
- `internal/metrics`: contadores e histórico.
- `internal/api`: API REST e dashboard.
- `compose.yaml`: publicação dos containers e volume compartilhado.

Explicar backpressure de forma simples: se o processamento ficar lento, as
filas esperam em vez de perder eventos. O arquivo de log funciona como buffer
durável.

## 4:00–5:15 — Interface para usuários leigos

No `http://localhost:8080`, selecionar o modo leigo.

Explicar:

- "Acessos" substitui o termo requisições para facilitar a leitura.
- "Páginas com problema" representa erros HTTP.
- "Tempo de espera" representa a latência média.
- "Como está o site?" oferece uma leitura textual da situação.
- A opção "Todo o período" mostra todo o histórico armazenado, não só os
  últimos 60 segundos.

Mostrar:

1. Alterar o período.
2. Usar o filtro de porta.
3. Buscar por `8082`, `/api` ou uma resposta específica.
4. Limpar todos os filtros.

## 5:15–6:30 — Interface avançada

Alternar para o modo avançado.

Mostrar:

- distribuição de status HTTP;
- endpoints mais acessados;
- IPs mais ativos;
- alertas recentes;
- goroutines;
- filas brutas e de eventos;
- linhas lidas, inválidas e agregadas;
- memória e versão do runtime.

Explicar que os dados técnicos ajudam uma equipe a investigar gargalos e
comportamentos inesperados. Os alertas são heurísticos e podem indicar pico
de um usuário, crawler ou teste de carga.

## 6:30–8:00 — Demonstração prática principal

Abrir uma das páginas, de preferência `http://localhost:8082`.

Demonstrar nesta ordem:

1. Mostrar que a página tem os links para `/api`, `/slow`, `/missing` e
   `/error`.
2. Abrir o botão **Gerar testes deste site**.
3. Clicar uma vez e explicar que o navegador vai fazer tráfego misto por
   30 segundos.
4. Voltar ao Observer em `http://localhost:8080`.
5. Deixar o gráfico de acessos, os indicadores e os alertas atualizarem.
6. Usar o filtro da porta 8082 para separar esse teste dos demais.
7. Voltar ao site e aguardar o resumo final de sucessos e problemas.

O teste gera respostas normais, lentas, inexistentes e de erro. Ele existe
para tornar a demonstração evidente sem exigir terminal ou conhecimento de
códigos HTTP.

## 8:00–9:00 — Demonstração técnica adicional

Mostrar uma segunda evidência prática:

```bash
docker compose exec nginx tail -n 5 /var/log/observer/access.jsonl
```

Explicar que cada linha contém porta, IP, método, caminho, status, bytes e
tempo de resposta.

Mostrar a API:

```bash
curl -s http://localhost:8080/api/v1/metrics
```

Destacar a separação entre métricas acumuladas, métricas da janela de 60
segundos e histórico completo.

Opcional, caso haja tempo:

```bash
docker compose run --rm loadgen -scenario spike -n 400
```

Esse cenário concentra requisições em um IP e pode disparar uma regra de alerta.

## 9:00–9:45 — Qualidade e exemplos

Mostrar rapidamente:

- testes unitários;
- testes de concorrência;
- race detector;
- fuzzing do parser;
- teste de integração com Docker Compose;
- benchmark com Go e Python;
- desligamento gracioso;
- limitação de memória;
- contagem exata dos eventos.

Fala sugerida:

"A qualidade não depende apenas da aparência. O projeto testa a concorrência,
erros de entrada, limites de memória, integração dos containers e o encerramento
correto dos processos."

## 9:45–10:00 — Fechamento

Fala sugerida:

"O projeto combina NGINX, Go e Docker para entregar observabilidade no mesmo
ambiente da aplicação. O sistema funciona para iniciantes porque apresenta
acessos e problemas em linguagem simples. Também atende equipes técnicas por
meio de filtros, APIs e detalhes do pipeline. Go foi uma escolha adequada
porque a aplicação é concorrente, contínua, pequena e distribuída como
container."

## Roteiro rápido de demonstração

Usar este fluxo quando o tempo estiver curto:

1. Abrir `http://localhost:8082` e clicar em **Gerar testes deste site**.
2. Abrir `http://localhost:8080`.
3. Mostrar o modo leigo e o gráfico avançando.
4. Selecionar a porta 8082 no filtro.
5. Alternar para o modo avançado.
6. Mostrar status, endpoints, filas e alertas.
7. Executar o comando do log bruto ou mostrar a API.
8. Encerrar explicando por que Go foi escolhido.

## Checklist de qualidade do vídeo

### Preparação

- Reiniciar o Compose antes da gravação:

  ```bash
  docker compose down
  docker compose up -d --build
  ```

- Confirmar que todas as portas respondem:

  ```bash
  curl -I http://localhost:8081
  curl -I http://localhost:8082
  curl -I http://localhost:8083
  curl -I http://localhost:8084
  curl -I http://localhost:8080/healthz
  ```

- Manter o terminal em fonte grande.
- Abrir o Observer e os sites em abas separadas antes de iniciar.
- Fechar notificações e aplicações que possam aparecer na tela.

### Gravação

- Mostrar o ponteiro ou destacar elementos antes de clicar.
- Evitar deixar espera sem explicação.
- Usar áudio claro e pausas curtas.
- Mostrar o nome completo da linguagem e o motivo da escolha.
- Mostrar o projeto funcionando antes de aprofundar o código.
- Usar zoom ou resolução alta para códigos e filtros serem legíveis.
- Não apresentar detalhes internos sem explicar o valor para o usuário.

### Qualidade

- Gravar em pelo menos 1080p.
- Usar 30 frames por segundo.
- Cortar pausas longas, erros de carregamento e repetições.
- Manter uma única linguagem visual entre a apresentação e o vídeo.
- Revisar as legendas e corrigir termos em inglês:
  goroutines, channels, backpressure, sidecar e tailer.
- Terminar com o fechamento e não deixar o teste continuar fora de contexto.
