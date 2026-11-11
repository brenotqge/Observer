#!/usr/bin/env bash
# Teste de integração ponta a ponta com Docker Compose.
#
# Prova que: NGINX inicia; Observer inicia; NGINX produz eventos; o Go os
# consome; a API reflete os dados; o dashboard é servido; as duas regras de
# alerta disparam. Requer apenas Docker + curl (não precisa de Go no host).
#
# Uso: ./scripts/integration-test.sh            (mantém os serviços no final)
#      KEEP=0 ./scripts/integration-test.sh     (derruba tudo ao final)
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1

OBS="http://127.0.0.1:${OBSERVER_PORT:-8080}"
NGX="http://127.0.0.1:${NGINX_PORT:-8081}"
pass=0; fail=0
ok()   { echo "  [OK]   $*"; pass=$((pass+1)); }
bad()  { echo "  [FALHA] $*"; fail=$((fail+1)); }
# Extrai o primeiro valor numérico de uma chave JSON (saída indentada da API).
num()  { grep -o "\"$1\": *[0-9.e+-]*" | head -1 | sed 's/.*: *//'; }

cleanup() { if [ "${KEEP:-1}" = "0" ]; then docker compose --profile tools down -v >/dev/null 2>&1 || true; fi; }
trap cleanup EXIT

echo "==> Subindo ambiente limpo (docker compose up --build)"
docker compose --profile tools down -v >/dev/null 2>&1 || true
docker compose up --build -d --wait --wait-timeout 120
docker compose --profile tools build loadgen >/dev/null

echo "==> 1. Serviços"
[ "$(docker compose ps --format '{{.Health}}' nginx)" = "healthy" ] && ok "nginx healthy" || bad "nginx não está healthy"
[ "$(docker compose ps --format '{{.Health}}' observer)" = "healthy" ] && ok "observer healthy" || bad "observer não está healthy"
# (sem "-o /dev/null": no Git Bash com MSYS_NO_PATHCONV o curl.exe não o entende)
code=$(curl -s -w '\n%{http_code}' "$NGX/" | tail -n1); [ "$code" = 200 ] && ok "NGINX responde 200" || bad "NGINX respondeu $code"
curl -s "$OBS/healthz" | grep -q '"log_file_present": true' && ok "observer enxerga o arquivo de log (volume ro)" || bad "arquivo de log não visível"

echo "==> 2. API vazia é válida"
curl -s "$OBS/api/v1/alerts" | grep -q '"items": \[\]' && ok "/alerts vazio retorna []" || bad "/alerts vazio inválido"

echo "==> 3. Tráfego normal (1000 req)"
docker compose run --rm loadgen -scenario normal -n 1000 -c 10 | sed 's/^/     /'
lines=$(docker compose exec -T nginx sh -c 'wc -l < /var/log/observer/access.jsonl' | tr -d ' \r')
[ "$lines" -ge 1000 ] && ok "NGINX gravou $lines linhas JSON" || bad "NGINX gravou só $lines linhas"
sleep 2
m=$(curl -s "$OBS/api/v1/metrics")
total=$(echo "$m" | num total_requests)
[ "${total%.*}" -ge 1000 ] && ok "API: total_requests=$total" || bad "API: total_requests=$total (< 1000)"
perr=$(echo "$m" | num parse_errors)
[ "$perr" = 0 ] && ok "nenhum erro de parsing" || bad "parse_errors=$perr"
rps=$(echo "$m" | num requests_per_second)
ok "requests_per_second (janela 60 s) = $rps"
curl -s "$OBS/api/v1/status-codes" | grep -q '"code": 500' && ok "status 500 contabilizado" || bad "sem 500 em /status-codes"
curl -s "$OBS/api/v1/top-endpoints" | grep -q '"key": "GET /api/items"' && ok "top-endpoints contém GET /api/items" || bad "top-endpoints incorreto"
curl -s "$OBS/api/v1/top-ips" | grep -Eq '"key": "(192\.0\.2|198\.51\.100|203\.0\.113)\.' && ok "top-ips mostra IPs fictícios (realip + XFF)" || bad "top-ips sem IPs TEST-NET"

echo "==> 4. Dashboard"
curl -s "$OBS/" | grep -q "GoNginx Observer" && ok "dashboard HTML servido" || bad "dashboard não servido"
curl -s "$OBS/app.js" | grep -q "api/v1/metrics" && ok "dashboard consulta a API" || bad "app.js ausente"

echo "==> 5. Regra A — pico de um único IP (400 req)"
docker compose run --rm loadgen -scenario spike -n 400 -c 5 | sed 's/^/     /'
sleep 2
curl -s "$OBS/api/v1/alerts" | grep -q '"type": "ip_request_rate"' && ok "alerta ip_request_rate gerado" || bad "alerta por IP não gerado"

echo "==> 6. Regra B — taxa de 5xx (1500 req, 60% erro)"
docker compose run --rm loadgen -scenario errors -n 1500 -c 10 | sed 's/^/     /'
sleep 2
curl -s "$OBS/api/v1/alerts" | grep -q '"type": "server_error_rate"' && ok "alerta server_error_rate gerado" || bad "alerta de 5xx não gerado"

echo "==> 7. Recursos do Observer"
docker stats --no-stream --format '     {{.Name}}: CPU {{.CPUPerc}}  MEM {{.MemUsage}}' gonginx-observer-observer-1 gonginx-observer-nginx-1
docker image ls --format '     {{.Repository}}:{{.Tag}} {{.Size}}' | grep gonginx-observer

echo "==> 8. Desligamento gracioso (SIGTERM)"
start=$(date +%s)
docker compose stop observer >/dev/null
elapsed=$(( $(date +%s) - start ))
exitcode=$(docker inspect -f '{{.State.ExitCode}}' gonginx-observer-observer-1)
[ "$exitcode" = 0 ] && ok "observer saiu com código 0 em ${elapsed}s" || bad "código de saída $exitcode"
docker compose logs observer 2>/dev/null | grep -q "desligamento concluído" && ok "log confirma desligamento concluído" || bad "sem log de desligamento"
docker compose start observer >/dev/null

echo
echo "Resultado: $pass OK, $fail falha(s)"
[ "$fail" = 0 ]
