#!/usr/bin/env bash
# Stage 1: three naive servers that copy writes to each other, best-effort.
#  (a) a node that was down misses writes forever (no catch-up)
#  (b) concurrent writes to different nodes leave nodes disagreeing (no order)
set -euo pipefail
cd "$(dirname "$0")/.."
make -s build
BIN=./bin/naivekv
PORTS=(8081 8082 8083)
PIDS=("" "" "")

peers_of() { local me=$1 out="" p; for p in "${PORTS[@]}"; do [ "$p" != "$me" ] && out="$out,http://127.0.0.1:$p"; done; echo "${out#,}"; }
start() { $BIN --http 127.0.0.1:${PORTS[$1]} --peers "$(peers_of ${PORTS[$1]})" 2>/dev/null & PIDS[$1]=$!; }
cleanup() { for p in "${PIDS[@]}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

for i in 0 1 2; do start $i; done; sleep 0.5
echo "== 3 nodes up on ${PORTS[*]}"

echo
echo "== (a) kill node 8083, write k1 to node 8081, restart 8083"
kill -9 ${PIDS[2]}; wait ${PIDS[2]} 2>/dev/null || true
curl -s -o /dev/null -w "PUT k1 on 8081 -> %{http_code}\n" -X PUT --data v1 http://127.0.0.1:8081/kv/k1
start 2; sleep 0.5
for p in "${PORTS[@]}"; do
  printf "GET k1 on %s -> %s\n" $p "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$p/kv/k1)"
done

echo
echo "== (b) 10 rounds: PUT x=A to 8081 and x=B to 8082 at the same time"
diverged=0
for round in $(seq 1 10); do
  curl -s -o /dev/null -X PUT --data "A$round" http://127.0.0.1:8081/kv/x & a=$!
  curl -s -o /dev/null -X PUT --data "B$round" http://127.0.0.1:8082/kv/x & b=$!
  wait $a $b
  sleep 0.2
  v1=$(curl -s http://127.0.0.1:8081/kv/x); v2=$(curl -s http://127.0.0.1:8082/kv/x); v3=$(curl -s http://127.0.0.1:8083/kv/x)
  same=yes
  if [ "$v1" != "$v2" ] || [ "$v2" != "$v3" ]; then same=no; diverged=$((diverged+1)); fi
  printf "round %2d: 8081=%-4s 8082=%-4s 8083=%-4s agree=%s\n" $round "$v1" "$v2" "$v3" $same
done
echo "rounds where nodes disagree: $diverged / 10"
