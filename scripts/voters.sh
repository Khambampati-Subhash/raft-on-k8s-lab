#!/usr/bin/env bash
# voters.sh: print the leader's membership, marking each member alive/DEAD.
NS=raftlab
kubectl -n $NS exec deploy/raftwatch -- /raftctl --service kv.$NS.svc.cluster.local --port 8080 status | python3 -c '
import json, sys
rows = json.load(sys.stdin)
alive = {r["status"]["id"] for r in rows if r.get("status")}
for r in rows:
    s = r.get("status")
    if s and s["role"] == "leader":
        n = len(s["servers"])
        print("  leader %s, %d voters, majority = %d, alive = %d" % (s["id"], n, n // 2 + 1, sum(m["id"] in alive for m in s["servers"])))
        for m in s["servers"]:
            print("    %-5s %s" % (m["id"], "alive" if m["id"] in alive else "DEAD"))'
