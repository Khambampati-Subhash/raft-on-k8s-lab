#!/usr/bin/env bash
# E9c: kv-1 and kv-2 go dark (100% packet loss on all their traffic) for 50s,
# and while they're dark kv-0 loses its disk. kv-0 comes back empty, can reach
# nobody, and decides for itself whether to bootstrap.
set -euo pipefail
NS=raftlab
kubectl apply -f - >/dev/null <<YAML
apiVersion: chaos-mesh.org/v1alpha1
kind: NetworkChaos
metadata:
  name: e9c-dark-majority
  namespace: $NS
spec:
  action: loss
  mode: all
  duration: 50s
  loss: {loss: "100"}
  selector:
    namespaces: [$NS]
    expressionSelectors:
      - {key: statefulset.kubernetes.io/pod-name, operator: In, values: [kv-1, kv-2]}
YAML
echo "kv-1, kv-2: 100% packet loss for 50s"
sleep 3
"$(dirname "$0")/../scripts/wipe-pvc.sh" 0
