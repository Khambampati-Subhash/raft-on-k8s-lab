#!/usr/bin/env bash
# E10: a rolling restart of the StatefulSet under load (what every image
# upgrade or config change does). Pods restart one at a time, kv-2 first.
set -euo pipefail
NS=raftlab
for p in kv-0 kv-1 kv-2; do echo "target: $p"; done
kubectl -n $NS rollout restart sts/kv
kubectl -n $NS rollout status sts/kv --timeout=180s
