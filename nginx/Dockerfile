# NGINX Open Source oficial, sem alterações no código-fonte.
# Apenas adicionamos configuração e conteúdo estático de demonstração.
FROM nginx:1.28-alpine

COPY conf.d/default.conf /etc/nginx/conf.d/default.conf
COPY html/ /usr/share/nginx/html/

# Diretório do log estruturado (montado como volume nomeado no compose) e
# arquivo de 48 KiB usado pelo endpoint /slow. Valida a configuração no build.
RUN mkdir -p /var/log/observer /usr/share/nginx/demo \
 && head -c 49152 /dev/zero | tr '\0' 'x' > /usr/share/nginx/demo/payload.bin \
 && nginx -t
