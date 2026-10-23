#!/usr/bin/env bash
# run-loadgen.sh <runID> <duration>: start Job loadgen-<runID> in raftlab.
# runID must be lowercase (it is part of the Job name). The image is whatever
# the kv StatefulSet runs, so it is always one that is loaded on the nodes.
set -euo pipefail
RUN=${1:?runID}; DUR=${2:?duration}
NS=raftlab
IMG=$(kubectl -n $NS get sts kv -o jsonpath='{.spec.template.spec.containers[0].image}')
kubectl -n $NS apply -f - >/dev/null <<YAML
apiVersion: batch/v1
kind: Job
metadata:
  name: loadgen-$RUN
  labels: {app: loadgen}
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 3600
  template:
    metadata:
      labels: {app: loadgen}
    spec:
      restartPolicy: Never
      securityContext: {runAsNonRoot: true, runAsUser: 65532}
      containers:
        - name: loadgen
          image: $IMG
          command: [/loadgen]
          args: [--service=kv.$NS.svc.cluster.local, --port=8080, --duration=$DUR,
                 --stale-probe, --run-id=$RUN, --report=]
YAML
echo "job/loadgen-$RUN"
