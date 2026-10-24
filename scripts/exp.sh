#!/usr/bin/env bash
# exp.sh <ID>: run one chaos experiment under load and record everything.
#
#   t=0    label roles, start loadgen (120s) and a raftwatch event capture (150s)
#   t=20s  relabel (leadership may have moved) and inject: chaos/<ID>.sh if it
#          exists, else kubectl apply -f chaos/<ID>-*.yaml
#   ...    wait for the loadgen Job to finish (write phase, then verify)
#   exit   remove the chaos, write docs/experiments/results/<ID>-<ts>.log,
#          exit 0 if the Job succeeded (Lost 0), 1 otherwise
#
# Every run starts from a fresh cluster (namespace and PVCs deleted, redeployed),
# so experiments don't inherit each other's data or damage.
#
# Chaos YAMLs carry `duration: 40s`, so a fault injected at ~20s heals at
# ~60s, long before loadgen's verify phase starts at ~120s. Verify must never
# run under chaos; the delete on exit is only a safety net.
set -uo pipefail
cd "$(dirname "$0")/.."
ID=${1:?experiment ID, e.g. E1}
NS=raftlab
SVC=kv.$NS.svc.cluster.local
TS=$(python3 -c 'import datetime; print(datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dt%H%M%S"))')
RUN=$(echo "$ID" | tr '[:upper:]' '[:lower:]')-$TS
LOG=docs/experiments/results/$ID-$TS.log
TMP=$(mktemp -d)
now() { python3 -c 'import datetime; print(datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="milliseconds"))'; }
rctl() { kubectl -n $NS exec deploy/raftwatch -- /raftctl --service $SVC --port 8080 "$@"; }
SCRIPT=chaos/$ID.sh
YAMLS=$(ls chaos/"$ID"-*.yaml 2>/dev/null || true)
[ -x "$SCRIPT" ] || [ -n "$YAMLS" ] || { echo "no chaos/$ID.sh or chaos/$ID-*.yaml"; exit 2; }
STATUS=1
# pods: name uid restarts last-termination-reason, one per line
pods() { kubectl -n $NS get pods -l app=kv -o jsonpath='{range .items[*]}{.metadata.name} {.metadata.uid} {.status.containerStatuses[0].restartCount} {.status.containerStatuses[0].lastState.terminated.reason}{"\n"}{end}'; }

finish() {
  if [ -z "${STARTED:-}" ]; then rm -rf "$TMP"; echo "$ID: aborted before loadgen started, no log written"; return; fi
  [ -n "$YAMLS" ] && kubectl delete -f $YAMLS --ignore-not-found >/dev/null 2>&1
  [ -x "chaos/$ID-heal.sh" ] && "chaos/$ID-heal.sh" >/dev/null 2>&1
  # Bounded wait for the raftwatch capture only (a bare `wait` once stalled
  # this script for ~16 min after the Job had finished).
  if [ -n "${WATCH_PID:-}" ]; then
    for _ in $(seq 1 30); do kill -0 "$WATCH_PID" 2>/dev/null || break; sleep 1; done
    kill "$WATCH_PID" 2>/dev/null
  fi
  pods > "$TMP/pods-after" 2>/dev/null
  # Any kv pod that restarted or was replaced but isn't named by the chaos
  # records is an unplanned disruption that pollutes this run's numbers.
  WARN=$(python3 - "$TMP/pods-before" "$TMP/pods-after" "$TMP/killed" <<'PY'
import sys
def load(p):
    try: return {l.split()[0]: l.split()[1:] for l in open(p) if l.strip()}
    except FileNotFoundError: return {}
before, after = load(sys.argv[1]), load(sys.argv[2])
try: killed = set(open(sys.argv[3]).read().split())
except FileNotFoundError: killed = set()
out = []
for name, (uid, restarts, *reason) in after.items():
    b = before.get(name)
    if not b: continue
    if b[0] != uid and name not in killed:
        out.append(f"{name} replaced (not by chaos)")
    elif b[0] == uid and int(restarts) > int(b[1]):
        out.append(f"{name} restarted {int(restarts) - int(b[1])}x ({' '.join(reason) or '?'})")
print("; ".join(out))
PY
)
  # If the host slept, the Docker VM froze mid-run: Raft's behaviour is still
  # valid, but every timing (gaps, election time) is not. Detect it from the
  # wall-clock length of loadgen's 120s write phase.
  SUSP=$(kubectl -n $NS logs "job/loadgen-$RUN" --timestamps 2>/dev/null | python3 -c '
import sys, datetime
t = {}
for l in sys.stdin:
    ts, _, msg = l.partition(" ")
    for k in ("loadgen: run", "loadgen: verifying"):
        if msg.startswith(k) and k not in t:
            t[k] = datetime.datetime.fromisoformat(ts[:26] + "+00:00")
if len(t) == 2:
    wall = (t["loadgen: verifying"] - t["loadgen: run"]).total_seconds()
    if wall > 135:
        print(f"write phase took {wall:.0f}s wall for 120s: host/VM was suspended, timings invalid")
')
  # A follower can look healthy (heartbeats, no elections) while not
  # replicating at all (E8b). Require every commit index to reach the leader's.
  CONV=""
  for _ in $(seq 1 15); do
    CONV=$(rctl status 2>/dev/null | python3 -c '
import json, sys
rows = [r["status"] for r in json.load(sys.stdin) if r.get("status")]
lead = [s["commitIndex"] for s in rows if s["role"] == "leader"]
if not lead: print("no leader after the run"); sys.exit()
print("; ".join("%s %d entries behind" % (s["id"], lead[0] - s["commitIndex"]) for s in rows if s["commitIndex"] < lead[0]))
')
    [ -z "$CONV" ] && break
    sleep 1
  done
  [ -n "$CONV" ] && SUSP="${SUSP:+$SUSP; }not converged: $CONV"
  WARN=$(printf "%s" "$WARN${WARN:+${SUSP:+; }}$SUSP")
  RESULT=$([ $STATUS = 0 ] && echo PASS || echo FAIL)${WARN:+ (WARN: $WARN)}
  {
    echo "# experiment $ID  run $RUN"
    echo; echo "## kv pods before (name uid restarts last-termination)"; cat "$TMP/pods-before" 2>/dev/null
    echo; echo "## raftctl status before"; cat "$TMP/before" 2>/dev/null
    echo; echo "## injection"; cat "$TMP/inject" 2>/dev/null
    echo; echo "## raftwatch events"; cat "$TMP/events" 2>/dev/null
    echo; echo "## loadgen"; kubectl -n $NS logs "job/loadgen-$RUN" 2>&1
    echo; echo "## kv pods after"; cat "$TMP/pods-after" 2>/dev/null
    echo; echo "## raftctl status after"; rctl status 2>&1
    echo; echo "## result: $RESULT (job status $STATUS)"
  } > "$LOG"
  rm -rf "$TMP"
  echo "$ID: $RESULT -> $LOG"
}
trap finish EXIT

echo "$ID: resetting cluster (fresh namespace and PVCs)"
make -s undeploy deploy >/dev/null 2>&1 || { echo "deploy failed"; exit 2; }
kubectl -n $NS rollout status sts/kv --timeout=180s >/dev/null && kubectl -n $NS rollout status deploy/raftwatch --timeout=60s >/dev/null || exit 2
for _ in $(seq 1 60); do
  rctl status 2>/dev/null | python3 -c 'import json,sys; r=[x["status"] for x in json.load(sys.stdin) if x.get("status")]; sys.exit(0 if len(r)==3 and sum(s["role"]=="leader" for s in r)==1 and all(len(s["servers"] or [])==3 for s in r) else 1)' && break
  sleep 2
done
pods > "$TMP/pods-before"
rctl status > "$TMP/before"
scripts/label-leader.sh >/dev/null
scripts/run-loadgen.sh "$RUN" 120s >/dev/null
STARTED=1
# kubectl itself (not a wrapper function) so `wait` tracks the real process.
kubectl -n $NS exec deploy/raftwatch -- /raftwatch --service $SVC --port 8080 --events-only --for 150s > "$TMP/events" 2>&1 &
WATCH_PID=$!
echo "$ID: loadgen-$RUN started, injecting in 20s"
sleep 20

{
  echo "leader before injection: $(scripts/label-leader.sh)"
  kubectl -n $NS get pods -l app=kv -L raft-role,raft-rank --no-headers | awk '{print "  "$1, $3, $6, $7}'
  echo "injected at $(now)"
  if [ -x "$SCRIPT" ]; then "$SCRIPT"; else kubectl apply -f $YAMLS; fi
} > "$TMP/inject" 2>&1
cat "$TMP/inject"

for _ in $(seq 1 120); do # write phase 120s + verify, poll every 2s, cap 240s
  s=$(kubectl -n $NS get job "loadgen-$RUN" -o jsonpath='{.status.succeeded}/{.status.failed}')
  case $s in 1/*) STATUS=0; break ;; */1) STATUS=1; break ;; esac
  sleep 2
done
if [ -n "$YAMLS" ]; then # which pods the chaos hit, from its own records
  kubectl get -f $YAMLS -o json 2>/dev/null | python3 -c '
import json, sys
d = json.load(sys.stdin)
for o in d.get("items", [d]):
    for r in o.get("status", {}).get("experiment", {}).get("containerRecords") or []:
        print(o["metadata"]["name"], r.get("id"), "phase=%s injected=%s" % (r.get("phase"), r.get("injectedCount")))
' > "$TMP/records"
  sed 's/^/chaos record: /' "$TMP/records" >> "$TMP/inject"
  awk '{print $2}' "$TMP/records" | sed 's|.*/||' > "$TMP/killed"
fi
exit $STATUS
