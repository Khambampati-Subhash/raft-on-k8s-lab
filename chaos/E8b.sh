#!/usr/bin/env bash
# E8b: half of one follower's log appends fail with EIO. Chaos Mesh IOChaos
# can't run on arm64 (see chaos/unsupported/), so this uses kvnode's in-process
# log-store fault injector. Holds the fault for 40s, then turns it off.
# Run by scripts/exp.sh at injection time (after it labelled raft-rank=f1).
set -euo pipefail
NS=raftlab
rctl() { kubectl -n $NS exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 "$@"; }
T=$(kubectl -n $NS get pods -l raft-rank=f1 -o jsonpath='{.items[0].metadata.name}')
echo "target: $T (follower f1)"
rctl fault "$T" --fail-rate 0.5
sleep 40
rctl fault "$T"
