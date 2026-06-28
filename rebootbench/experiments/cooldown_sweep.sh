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
RUNTIME="${RUNTIME:-docker}"             # docker | podman
CONTAINER="${CONTAINER:-rebootbench-nginx}"
IMAGE="${IMAGE:-nginx:alpine}"           # podman は明示的に docker.io/... を要するので
                                          # RUNTIME=podman 時は IMAGE_PODMAN を優先
IMAGE_PODMAN="${IMAGE_PODMAN:-docker.io/library/nginx:alpine}"
PORT="${PORT:-18080}"
TRIALS="${TRIALS:-30}"
INTERVAL="${INTERVAL:-10ms}"
PROBE_TIMEOUT="${PROBE_TIMEOUT:-8ms}"
PRE_SETTLE="${PRE_SETTLE:-800ms}"
POST_SETTLE="${POST_SETTLE:-300ms}"
COOLDOWNS="${COOLDOWNS:-1s 2s 3s 5s 8s}"
INJECTOR="${INJECTOR:-restart}"          # kill | kill-start | restart
KILL_START_DELAY="${KILL_START_DELAY:-0}"
NOTES_PREFIX="${NOTES_PREFIX:-Phase 0.5 cooldown sweep}"

img() {
  if [ "$RUNTIME" = "podman" ]; then echo "$IMAGE_PODMAN"; else echo "$IMAGE"; fi
}

reset_container() {
  "$RUNTIME" rm -f "$CONTAINER" >/dev/null 2>&1 || true
  "$RUNTIME" run -d --name "$CONTAINER" --restart=always -p "$PORT:80" "$(img)" >/dev/null
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
    --runtime "$RUNTIME" \
    --container "$CONTAINER" \
    --url "http://localhost:$PORT/" \
    --trials "$TRIALS" \
    --interval "$INTERVAL" \
    --probe-timeout "$PROBE_TIMEOUT" \
    --pre-settle "$PRE_SETTLE" \
    --post-settle "$POST_SETTLE" \
    --cooldown "$cd" \
    --injector "$INJECTOR" \
    --restart-grace 0 \
    --kill-start-delay "$KILL_START_DELAY" \
    --db "$DB" \
    --notes "$NOTES_PREFIX runtime=$RUNTIME injector=$INJECTOR cooldown=$cd" \
    2>&1 | tail -12
  echo "===== cooldown=$cd done:  $(date) ====="
  echo
done

"$RUNTIME" rm -f "$CONTAINER" >/dev/null 2>&1 || true
echo "sweep complete ($RUNTIME): $(date)"
