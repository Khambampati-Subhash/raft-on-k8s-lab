#!/usr/bin/env bash
# Stage 3: Raft on plain Deployments, and the two ways it falls apart.
# Expects `make cluster-up image load-image`. Recreates namespace raftlab.
set -euo pipefail
cd "$(dirname "$0")/.."
NS=raftlab
IMAGE=${IMAGE:-raftlab:$(git describe --always --dirty)}
k() { kubectl -n $NS "$@"; }
rctl() { k exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 "$@"; }

# status: one line per reachable pod, then the leader's view of membership
# with each member marked alive/dead (dead = no pod answers with that ID).
status() {
  rctl status | python3 -c '
import json, sys
rows = json.load(sys.stdin)
alive = {r["status"]["id"] for r in rows if r.get("status")}
for r in rows:
    s = r.get("status")
    if not s: print("  %-24s unreachable" % r["url"]); continue
    print("  %-24s role=%-9s term=%-3s leader=%-24s servers=%d" % (s["id"], s["role"], s["term"], s["leaderId"] or "-", len(s["servers"] or [])))
for r in rows:
    s = r.get("status")
    if s and s["role"] == "leader":
        print("  membership according to %s:" % s["id"])
        for m in s["servers"]:
            print("    %-24s %-18s %s" % (m["id"], m["addr"], "alive" if m["id"] in alive else "DEAD"))
'
}
wait_for() { # wait_for <description> <python predicate over rows; n(s) = member count>
  for _ in $(seq 1 90); do
    rctl status 2>/dev/null | python3 -c "import json,sys; rows=[r['status'] for r in json.load(sys.stdin) if r.get('status')]; n=lambda s: len(s['servers'] or []); sys.exit(0 if ($2) else 1)" && return 0
    sleep 2
  done
  echo "timed out waiting for: $1"; status; exit 1
}
pod_of() { k get pod -l "app=kv,role=$1" -o jsonpath='{.items[*].metadata.name}'; }
http() { # http <pod> <curl args with URL on $PF>: talk to one pod through a fresh port-forward
  local pod=$1; shift
  PF=$((20000 + RANDOM % 10000))
  # kubectl itself, not the k() wrapper: `k ... &` would background a subshell
  # and `kill $!` would leave the real port-forward running.
  kubectl -n $NS port-forward "pod/$pod" $PF:8080 >/dev/null 2>&1 & local pf=$!
  for _ in $(seq 1 50); do curl -s -o /dev/null http://127.0.0.1:$PF/healthz && break; sleep 0.1; done
  curl -s "${@//PORT/$PF}" || true
  kill $pf; wait $pf 2>/dev/null || true
}

echo "== deploy (seed bootstraps, two rest pods join it)"
kubectl delete ns $NS --ignore-not-found --wait >/dev/null
kubectl kustomize deploy/stage3-deployment | sed "s|image: raftlab:dev|image: $IMAGE|" | kubectl apply -f - >/dev/null
k rollout status deploy/raftwatch --timeout=120s >/dev/null
wait_for "1 leader, 3 voters" 'sum(s["role"]=="leader" for s in rows)==1 and len(rows)==3 and all(n(s)==3 for s in rows)'
k get pods -o wide | awk '{print "  "$1, $6, $7}'
status

SEED=$(pod_of seed)
echo
echo "== write city=paris and lang=go through the leader ($SEED)"
for kv in city=paris lang=go; do
  http "$SEED" -o /dev/null -w "  PUT ${kv%%=*} -> %{http_code}\n" -X PUT --data "${kv#*=}" "http://127.0.0.1:PORT/kv/${kv%%=*}"
done

echo
new_pod() { # new_pod <role> <old pod names...>: wait for a replacement and print its name
  local role=$1; shift
  for _ in $(seq 1 60); do
    for p in $(pod_of "$role"); do [[ " $* " == *" $p "* ]] || { echo "$p"; return; }; done
    sleep 1
  done
}
REST=($(pod_of rest))

echo "== (a) delete one kv-rest pod (${REST[0]}); the Deployment replaces it"
k delete pod "${REST[0]}" --wait=false >/dev/null
R1=$(new_pod rest "${REST[@]}")
wait_for "$R1 joined" 'any(s["id"]=="'"$R1"'" and n(s)==4 for s in rows) and sum(s["role"]=="leader" for s in rows)==1'
k get pods -o wide | awk '{print "  "$1, $6, $7}'
status
http "$SEED" -o /dev/null -w "  PUT color=blue -> %{http_code}\n" -X PUT --data blue "http://127.0.0.1:PORT/kv/color"

echo
echo "== (b) delete the other original kv-rest pod (${REST[1]})"
k delete pod "${REST[1]}" --wait=false >/dev/null
R2=$(new_pod rest "${REST[@]}" "$R1")
sleep 30 # give it every chance to join
k get pods -o wide | awk '{print "  "$1, $2, $6, $7}'
status
echo "  last join attempts from $R2:"
k logs "$R2" | grep 'join:' | tail -3 | sed 's/^/    /'
http "$SEED" -o /dev/null -w "  PUT color=red -> %{http_code}\n" --max-time 10 -X PUT --data red "http://127.0.0.1:PORT/kv/color"

echo
echo "== (c) delete the seed pod ($SEED); its replacement starts empty and bootstraps again"
k delete pod "$SEED" --wait=false >/dev/null
NEW_SEED=$(new_pod seed "$SEED")
k wait --for=condition=Ready "pod/$NEW_SEED" --timeout=120s >/dev/null
sleep 5
k get pods -o wide | awk '{print "  "$1, $2, $6, $7}'
status
echo "  read city on the new seed ($NEW_SEED), the pod behind Service kv-seed:"
http "$NEW_SEED" -w "  -> %{http_code}\n" -o /dev/null "http://127.0.0.1:PORT/kv/city"
for p in $(pod_of rest); do
  echo "  stale reads on $p (a member of the old cluster):"
  for key in city color; do http "$p" -w " ($key -> %{http_code})\n" "http://127.0.0.1:PORT/kv/$key?stale=true"; done
done

echo
echo "== raftwatch events"
k logs deploy/raftwatch | tail -25
