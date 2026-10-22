#!/usr/bin/env bash
# Stage 0: one naive in-memory server. Kill it and the data is gone.
set -euo pipefail
cd "$(dirname "$0")/.."
make -s build
BIN=./bin/naivekv
URL=http://127.0.0.1:8081

start() { $BIN --http 127.0.0.1:8081 2>/dev/null & PID=$!; sleep 0.5; }
trap 'kill $PID 2>/dev/null || true' EXIT

echo "== start one naivekv"
start
for k in alpha beta gamma; do
  curl -s -o /dev/null -w "PUT $k -> %{http_code}\n" -X PUT --data "value-of-$k" $URL/kv/$k
done
echo "GET beta -> $(curl -s $URL/kv/beta)"

echo "== kill -9 the server (pid $PID)"
kill -9 $PID; wait $PID 2>/dev/null || true
echo "GET beta while down -> $(curl -s -o /dev/null -w '%{http_code}' $URL/kv/beta || true) (000 = connection refused)"

echo "== restart it"
start
for k in alpha beta gamma; do
  curl -s -o /dev/null -w "GET $k -> %{http_code}\n" $URL/kv/$k
done
