#!/usr/bin/env bash
# Executado dentro do container Python (mesmo ambiente para as duas linguagens).
set -euo pipefail
OUT=/bench/results
# O dataset é copiado para o sistema de arquivos do container: bind mounts do
# Docker Desktop (Windows/macOS) têm I/O lento e distorceriam as medições.
DATA=/tmp/dataset.jsonl
cp "/bench/data/dataset-$N.jsonl" "$DATA"
: > "$OUT/runs.jsonl"

python3 --version > "$OUT/python-version.txt"
lscpu 2>/dev/null | grep -E 'Model name|^CPU\(s\)' > "$OUT/cpu.txt" || grep -m1 'model name' /proc/cpuinfo > "$OUT/cpu.txt"

# Aquecimento do cache de páginas do SO (o arquivo fica em memória para
# todas as execuções, isolando o custo de CPU do parsing).
cat "$DATA" > /dev/null

run() { # $1 = rótulo, resto = comando
  local label=$1; shift
  "$@" > "$OUT/out-$label.json" 2> "$OUT/err.tmp"
  tail -n1 "$OUT/err.tmp" >> "$OUT/runs.jsonl"
}

for i in $(seq 1 "$R"); do
  echo "   rodada $i/$R"
  run go1     /bench/bin/benchgo -in "$DATA" -workers 1
  run goN     /bench/bin/benchgo -in "$DATA" -workers "$W"
  run python  python3 /bench/python/aggregate.py "$DATA"
done

# Verificação: resultados idênticos byte a byte.
sha256sum "$OUT"/out-*.json | tee "$OUT/checksums.txt"
if [ "$(cut -d' ' -f1 "$OUT/checksums.txt" | sort -u | wc -l)" -ne 1 ]; then
  echo "ERRO: as implementações produziram resultados diferentes" >&2
  exit 1
fi
echo "   saídas idênticas nas 3 variantes"

# Inicialização: 30 processos que saem imediatamente (sem trabalho).
startup() {
  local s e; s=$(date +%s%N)
  for _ in $(seq 1 30); do "$@"; done
  e=$(date +%s%N)
  echo $(( (e - s) / 30000 ))  # microssegundos por processo
}
echo "go_us $(startup /bench/bin/benchgo -noop)" > "$OUT/startup.txt"
echo "python_us $(startup python3 /bench/python/aggregate.py --noop)" >> "$OUT/startup.txt"
echo "python_bare_us $(startup python3 -c pass)" >> "$OUT/startup.txt"
cat "$OUT/startup.txt"
