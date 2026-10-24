#!/usr/bin/env bash
# E11b: scale 3 -> 5, then 5 -> 3 the naive way (just change replicas).
# Repairs at the end: removes the dead members so the cluster is left clean.
set -euo pipefail
NS=raftlab
D=$(dirname "$0")/../scripts
rctl() { kubectl -n $NS exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 "$@"; }
kubectl -n $NS scale sts/kv --replicas=5
kubectl -n $NS rollout status sts/kv --timeout=180s
sleep 3
echo "at 5:"; "$D/voters.sh"
echo "target: kv-3"; echo "target: kv-4"
kubectl -n $NS scale sts/kv --replicas=3
kubectl -n $NS wait --for=delete pod/kv-3 pod/kv-4 --timeout=120s
sleep 3
echo "after naive scale-down to 3:"; "$D/voters.sh"
sleep 10
echo "repair: removing the dead members"
rctl leave kv-4; rctl leave kv-3
"$D/voters.sh"
