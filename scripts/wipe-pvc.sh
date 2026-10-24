#!/usr/bin/env bash
# wipe-pvc.sh <ordinal>: destroy kv-N's disk and let the StatefulSet bring it
# back empty. kind's local-path PVs have reclaim policy Delete, so the data is
# really gone. Prints what it did; used by chaos/E9a.sh and chaos/E9b.sh.
set -euo pipefail
NS=raftlab
P=kv-${1:?ordinal}
k() { kubectl -n $NS "$@"; }
OLD=$(k get pvc data-$P -o jsonpath='{.metadata.uid}')
echo "target: $P (old PVC uid $OLD)"
k delete pvc data-$P --wait=false >/dev/null   # pvc-protection holds it while the pod runs
k delete pod $P --wait=false >/dev/null
for _ in $(seq 1 120); do
  NEW=$(k get pvc data-$P -o jsonpath='{.metadata.uid} {.status.phase}' 2>/dev/null || true)
  [ -n "$NEW" ] && [ "${NEW% *}" != "$OLD" ] && [ "${NEW#* }" = Bound ] && break
  sleep 1
done
echo "new PVC: ${NEW:-none}"
k wait --for=condition=Ready pod/$P --timeout=120s >/dev/null && echo "$P Ready again at $(python3 -c 'import datetime;print(datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="milliseconds"))')"
sleep 5
k exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 status | python3 -c '
import json, sys
for r in json.load(sys.stdin):
    s = r.get("status")
    if not s: print("  %s unreachable" % r["url"]); continue
    print("  %s role=%s term=%s leader=%s commit=%s servers=%s" % (s["id"], s["role"], s["term"], s["leaderId"] or "-", s["commitIndex"], [m["id"] for m in s["servers"] or []]))'
