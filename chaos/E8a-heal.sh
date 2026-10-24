#!/usr/bin/env bash
# Safety net (scripts/exp.sh runs chaos/<ID>-heal.sh on exit): turn the
# log-store fault off on every node.
NS=raftlab
for id in kv-0 kv-1 kv-2; do
  kubectl -n $NS exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 fault "$id" >/dev/null 2>&1
done
