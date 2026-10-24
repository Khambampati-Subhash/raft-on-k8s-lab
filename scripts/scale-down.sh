#!/usr/bin/env bash
# scale-down.sh N: shrink the cluster to N members safely: remove each member
# with ordinal >= N from the Raft membership first (highest first, via the
# leader), then scale the StatefulSet. Scaling first would leave dead voters.
set -euo pipefail
NS=raftlab
N=${1:?target size}
CUR=$(kubectl -n $NS get sts kv -o jsonpath='{.spec.replicas}')
for ((i = CUR - 1; i >= N; i--)); do
  kubectl -n $NS exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 leave kv-$i
done
kubectl -n $NS scale sts/kv --replicas=$N
kubectl -n $NS rollout status sts/kv --timeout=180s
