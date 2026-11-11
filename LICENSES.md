# Licenças e atribuições

## Distinção entre os componentes

- **NGINX Open Source** é um software independente, desenvolvido pela F5, Inc.
  e pela comunidade (https://nginx.org, https://github.com/nginx/nginx). Este
  projeto **não modifica** o código-fonte do NGINX, não o redistribui em forma
  alterada e **não utiliza** nenhum componente do NGINX Plus. Usamos a imagem
  Docker oficial `nginx:1.28-alpine` sem alterações, adicionando apenas um
  arquivo de configuração e páginas estáticas de demonstração.
- **GoNginx Observer** (o código em `cmd/`, `internal/`, `bench/`, `scripts/`)
  é um serviço independente que apenas **lê os arquivos de log** produzidos
  pelo NGINX. Não há vínculo de código, linkagem ou módulo carregado no NGINX.
- "NGINX" é marca registrada da F5, Inc. O nome é usado aqui apenas de forma
  descritiva (para indicar compatibilidade); este projeto não é afiliado nem
  endossado pela F5.

## NGINX Open Source

- Licença: **BSD de 2 cláusulas** ("2-clause BSD-like license").
- Texto: https://nginx.org/LICENSE e arquivo `LICENSE` do repositório oficial.
- Atribuição exigida na redistribuição: manter o aviso de copyright, a lista de
  condições e o aviso de isenção de garantia. A imagem oficial já inclui esses
  avisos; este projeto não redistribui binários do NGINX fora dela.

## Dependências utilizadas

| Componente | Uso | Licença |
|---|---|---|
| Go (compilador e biblioteca padrão) | linguagem do Observer; embutida no binário | BSD de 3 cláusulas |
| Imagem `golang:1.25-bookworm` | apenas estágio de build (não distribuída) | Go: BSD-3; pacotes Debian: diversas |
| Imagem `gcr.io/distroless/static-debian12` | base da imagem final | projeto distroless: Apache 2.0; contém pacotes Debian (`base-files`, `tzdata`, `ca-certificates`, `netbase`) sob suas respectivas licenças |
| Imagem `nginx:1.28-alpine` | serviço NGINX | NGINX: BSD-2; Alpine Linux e pacotes: diversas (MIT, GPL etc.) |
| Imagem `python:3.13-slim` | somente benchmark | Python: PSF License; Debian: diversas |
| Python (biblioteca padrão) | `bench/python/aggregate.py` | PSF License |

**Nenhuma biblioteca de terceiros** é usada no código Go (o `go.mod` não tem
`require`) nem no código Python. O dashboard usa apenas HTML, CSS e JavaScript
escritos neste projeto, sem bibliotecas externas.

As imagens Docker contêm software de terceiros sob licenças próprias; quem
redistribuir as imagens geradas deve observar essas licenças.

## Licença deste projeto

O código do GoNginx Observer foi escrito para fins acadêmicos. Caso seja
publicado, sugere-se uma licença permissiva compatível com as dependências
(por exemplo, MIT ou BSD-3), definida pelos autores.
