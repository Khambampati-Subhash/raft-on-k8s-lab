#!/usr/bin/env bash
# E8a: every log append on the leader takes 200ms longer. Chaos Mesh IOChaos
# can't run on arm64 (see chaos/unsupported/), so this uses kvnode's in-process
# log-store fault injector. Holds the fault for 40s, then turns it off.
# Run by scripts/exp.sh at injection time.
set -euo pipefail
NS=raftlab
rctl() { kubectl -n $NS exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 "$@"; }
T=$(rctl leader)
echo "target: $T (leader)"
rctl fault "$T" --delay 200ms
sleep 40
rctl fault "$T"
