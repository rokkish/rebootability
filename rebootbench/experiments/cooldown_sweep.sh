#!/usr/bin/env bash
# Phase 0.5: cooldown 感度分析
#
# 同じ SUT (nginx:alpine, restart mode) に対して cooldown を変えながら
# 30 trial を回し、完走率と recovery time 統計を見る。
#
# 出力: $DB (default rebootbench.db) に各実験が experiment row として残る。
# 集計は別途 SQL で行う (sweep_summary.sql 参照)。

set -euo pipefail

BIN="${BIN:-./rebootbench}"
DB="${DB:-rebootbench.db}"
CONTAINER="${CONTAINER:-rebootbench-nginx}"
IMAGE="${IMAGE:-nginx:alpine}"
PORT="${PORT:-18080}"
TRIALS="${TRIALS:-30}"
INTERVAL="${INTERVAL:-10ms}"
PROBE_TIMEOUT="${PROBE_TIMEOUT:-8ms}"
PRE_SETTLE="${PRE_SETTLE:-800ms}"
POST_SETTLE="${POST_SETTLE:-300ms}"
COOLDOWNS="${COOLDOWNS:-1s 2s 3s 5s 8s}"

reset_container() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker run -d --name "$CONTAINER" --restart=always -p "$PORT:80" "$IMAGE" >/dev/null
  # warm-up: wait for nginx to actually serve 200
  for i in $(seq 1 20); do
    if curl -fsS -m 1 -o /dev/null "http://localhost:$PORT/"; then return; fi
    sleep 0.5
  done
  echo "warm-up failed" >&2
  exit 1
}

for cd in $COOLDOWNS; do
  echo "===== cooldown=$cd start: $(date) ====="
  reset_container
  "$BIN" phase0 \
    --container "$CONTAINER" \
    --url "http://localhost:$PORT/" \
    --trials "$TRIALS" \
    --interval "$INTERVAL" \
    --probe-timeout "$PROBE_TIMEOUT" \
    --pre-settle "$PRE_SETTLE" \
    --post-settle "$POST_SETTLE" \
    --cooldown "$cd" \
    --injector restart \
    --restart-grace 0 \
    --db "$DB" \
    --notes "Phase 0.5 cooldown sweep cooldown=$cd" \
    2>&1 | tail -12
  echo "===== cooldown=$cd done:  $(date) ====="
  echo
done

docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
echo "sweep complete: $(date)"
