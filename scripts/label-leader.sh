#!/usr/bin/env bash
# Label the current Raft roles onto the kv pods so chaos selectors can target
# them: leader → raft-role=leader,raft-rank=l; followers (sorted by ID) →
# raft-role=follower,raft-rank=f1 / f2. Prints the leader's ID.
set -euo pipefail
NS=raftlab
L=$(kubectl -n $NS exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 leader)
kubectl -n $NS label pods -l app=kv raft-role- raft-rank- >/dev/null
kubectl -n $NS label pod "$L" raft-role=leader raft-rank=l >/dev/null
i=0
for p in $(kubectl -n $NS get pods -l app=kv -o jsonpath='{.items[*].metadata.name}' | tr ' ' '\n' | sort); do
  [ "$p" = "$L" ] && continue
  i=$((i + 1))
  kubectl -n $NS label pod "$p" raft-role=follower raft-rank=f$i >/dev/null
done
echo "$L"
