#!/usr/bin/env bash
# Stage 2: kill the Raft leader and watch the cluster elect a new one
# without losing an acknowledged write. Expects `make local-up` to be running.
set -euo pipefail
DIR=${RAFTLAB_DIR:-${TMPDIR:-/tmp}/raftlab}
summary() { # one line per node: id role term leader commit
  for i in 1 2 3; do
    curl -s --max-time 1 http://127.0.0.1:808$i/status \
      | python3 -c 'import json,sys; s=json.load(sys.stdin); print("  %s: role=%-9s term=%s leader=%-3s commit=%s" % (s["id"], s["role"], s["term"], s["leaderId"] or "-", s["commitIndex"]))' \
      2>/dev/null || echo "  n$i: unreachable"
  done
}
leader() { for i in 1 2 3; do curl -s --max-time 1 http://127.0.0.1:808$i/status | grep -q '"role":"leader"' && { echo $i; return; }; done; }

echo "== cluster"
summary
L=$(leader); echo "leader is n$L"

echo
echo "== write city=paris to a follower (gets redirected), then to the leader"
F=$(( L % 3 + 1 ))
curl -s -D - -o /dev/null -X PUT --data paris http://127.0.0.1:808$F/kv/city | grep -iE '^HTTP|^x-raft-leader' | tr -d '\r'
curl -s -o /dev/null -w "PUT on leader n$L -> %{http_code}\n" -X PUT --data paris http://127.0.0.1:808$L/kv/city

echo
echo "== stale read on every node, immediately (no leader check)"
for i in 1 2 3; do printf "  n%s: %s\n" $i "$(curl -s "http://127.0.0.1:808$i/kv/city?stale=true")"; done
sleep 0.2
echo "== stale read on every node, 200 ms later"
for i in 1 2 3; do printf "  n%s: %s\n" $i "$(curl -s "http://127.0.0.1:808$i/kv/city?stale=true")"; done
echo "== linearizable read on a follower"
curl -s -o /dev/null -w "  n$F -> %{http_code} (421 = ask the leader)\n" http://127.0.0.1:808$F/kv/city

echo
T0=$(python3 -c 'import time; print(time.time())')
echo "== kill -9 leader n$L"
kill -9 "$(cat "$DIR/n$L.pid")"
NEW=""
while [ -z "$NEW" ] || [ "$NEW" = "$L" ]; do sleep 0.05; NEW=$(leader || true); done
T1=$(python3 -c 'import time; print(time.time())')
python3 -c "print(f'new leader n$NEW elected after {($T1-$T0)*1000:.0f} ms')"
summary

echo
echo "== read city through the new leader"
curl -s -w "  -> %{http_code}\n" http://127.0.0.1:808$NEW/kv/city
echo "== writes still work with 2 of 3 nodes"
curl -s -o /dev/null -w "  PUT city=rome on n$NEW -> %{http_code}\n" -X PUT --data rome http://127.0.0.1:808$NEW/kv/city

echo
echo "== restart n$L: it rejoins as a follower and catches up"
i=$L
nohup ./bin/kvnode --id n$i --http 127.0.0.1:808$i --raft-bind 127.0.0.1:700$i \
  --raft-advertise 127.0.0.1:700$i --data "$DIR/n$i" --bootstrap=false --join=http://127.0.0.1:808$NEW >>"$DIR/n$i.log" 2>&1 &
echo $! > "$DIR/n$i.pid"
for _ in $(seq 1 100); do [ "$(curl -s "http://127.0.0.1:808$i/kv/city?stale=true")" = rome ] && break; sleep 0.1; done
summary
printf "  stale read of city on n%s: %s\n" $L "$(curl -s "http://127.0.0.1:808$L/kv/city?stale=true")"
