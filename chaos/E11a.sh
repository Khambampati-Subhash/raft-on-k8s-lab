#!/usr/bin/env bash
# E11a: scale 3 -> 5 under load. kv-3 and kv-4 start, ask to join, and the
# leader adds them as voters.
set -euo pipefail
NS=raftlab
D=$(dirname "$0")/../scripts
kubectl -n $NS scale sts/kv --replicas=5
kubectl -n $NS rollout status sts/kv --timeout=180s
sleep 3
echo "after scale-up:"; "$D/voters.sh"
