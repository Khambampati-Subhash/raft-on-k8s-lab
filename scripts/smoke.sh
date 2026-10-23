#!/usr/bin/env bash
# Stage 4 smoke test: the StatefulSet cluster is healthy and survives a pod
# restart with its identity and data. Fails fast; exit 0 means all checks held.
set -euo pipefail
NS=raftlab
SVC=kv.$NS.svc.cluster.local
k() { kubectl -n $NS "$@"; }
rctl() { k exec deploy/raftwatch -- /raftctl --service $SVC --port 8080 "$@"; }
fail() { echo "FAIL: $*"; exit 1; }
ok() { echo "ok:   $*"; }
http() { # http <pod> <curl args with URL on PORT>: one request through a fresh port-forward
  local pod=$1; shift
  local port=$((20000 + RANDOM % 10000))
  kubectl -n $NS port-forward "pod/$pod" $port:8080 >/dev/null 2>&1 & local pf=$!
  for _ in $(seq 1 50); do curl -s -o /dev/null http://127.0.0.1:$port/healthz && break; sleep 0.1; done
  curl -s "${@//PORT/$port}" || true
  kill $pf; wait $pf 2>/dev/null || true
}
# check <description> <python predicate over rows = list of /status dicts>
check() {
  rctl status | python3 -c "
import json, sys
rows = [r['status'] for r in json.load(sys.stdin) if r.get('status')]
sys.exit(0 if ($2) else 1)" || { rctl status; fail "$1"; }
  ok "$1"
}

[ "$(k get svc kv -o jsonpath='{.spec.publishNotReadyAddresses}')" = true ] \
  || fail "Service kv must set publishNotReadyAddresses: true"
ok "Service kv publishes not-ready addresses"

k rollout status statefulset/kv --timeout=120s >/dev/null || fail "3/3 Ready within 120s"
k rollout status deploy/raftwatch --timeout=60s >/dev/null
ok "3/3 kv pods Ready"
[ "$(k get pods -l app=kv -o jsonpath='{range .items[*]}{.spec.nodeName}{"\n"}{end}' | sort -u | wc -l | tr -d ' ')" = 3 ] \
  || fail "kv pods must be on 3 distinct nodes"
ok "kv pods on 3 distinct nodes"

ONE_LEADER='len(rows) == 3 and sum(s["role"] == "leader" for s in rows) == 1 and len({s["term"] for s in rows}) == 1 and all(sorted(m["id"] for m in s["servers"] or []) == ["kv-0", "kv-1", "kv-2"] for s in rows)'
check "one leader, voters exactly kv-0..kv-2, one term" "$ONE_LEADER"

echo "....  loadgen 10s"
k exec deploy/raftwatch -- /loadgen --service $SVC --port 8080 --workers 4 --duration 10s \
  --run-id "smoke-$(date +%s)" --report "" | sed -n '/^run /,/Lost:/p' || fail "loadgen reported lost writes"
ok "loadgen: Lost 0"

LEADER=$(rctl leader)
MARK=$(date +%s%N)
[ "$(http "$LEADER" -o /dev/null -w '%{http_code}' -X PUT --data "$MARK" http://127.0.0.1:PORT/kv/smoke/marker)" = 204 ] \
  || fail "write smoke/marker through $LEADER"
for _ in $(seq 1 50); do [ "$(http kv-1 'http://127.0.0.1:PORT/kv/smoke/marker?stale=true')" = "$MARK" ] && break; sleep 0.1; done
UID_BEFORE=$(k get pod kv-1 -o jsonpath='{.metadata.uid}')
k delete pod kv-1 --wait=false >/dev/null
for _ in $(seq 1 60); do
  [ "$(k get pod kv-1 -o jsonpath='{.metadata.uid}' 2>/dev/null)" != "$UID_BEFORE" ] && break; sleep 1
done
k wait --for=condition=Ready pod/kv-1 --timeout=120s >/dev/null || fail "kv-1 Ready again"
ok "kv-1 replaced and Ready"
check "after restart: still one leader, voters exactly kv-0..kv-2 (no ghost)" "$ONE_LEADER"
GOT=$(http kv-1 'http://127.0.0.1:PORT/kv/smoke/marker?stale=true')
[ "$GOT" = "$MARK" ] || fail "stale read of smoke/marker on new kv-1: got '$GOT', want '$MARK'"
ok "new kv-1 pod serves smoke/marker from its PVC"
echo "SMOKE PASSED"
