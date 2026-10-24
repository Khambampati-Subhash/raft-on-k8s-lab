#!/usr/bin/env bash
# E11c: scale 3 -> 5, then 5 -> 3 the right way: leave first, then scale.
set -euo pipefail
NS=raftlab
D=$(dirname "$0")/../scripts
kubectl -n $NS scale sts/kv --replicas=5
kubectl -n $NS rollout status sts/kv --timeout=180s
sleep 3
echo "at 5:"; "$D/voters.sh"
echo "target: kv-3"; echo "target: kv-4"
"$D/scale-down.sh" 3
sleep 3
echo "after leave-first scale-down to 3:"; "$D/voters.sh"
