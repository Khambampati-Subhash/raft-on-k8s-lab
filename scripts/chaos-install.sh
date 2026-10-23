#!/usr/bin/env bash
# Install Chaos Mesh into the kind cluster.
#
# Images are pulled by the host's docker and side-loaded with `kind load`,
# because kind nodes inherit the host's HTTP(S)_PROXY, which may point at a
# loopback proxy that doesn't exist inside the node containers. Works the same
# on networks without a proxy. Dashboard and DNS server are off: no
# experiment uses them.
set -euo pipefail
V=${CHAOS_MESH_VERSION:?set CHAOS_MESH_VERSION}
ARCH=${ARCH:-arm64}
CLUSTER=${CLUSTER:-raftlab}

helm repo add chaos-mesh https://charts.chaos-mesh.org >/dev/null 2>&1 || true
helm repo update chaos-mesh >/dev/null

VALUES=(--set chaosDaemon.runtime=containerd
        --set chaosDaemon.socketPath=/run/containerd/containerd.sock
        --set controllerManager.replicaCount=1
        --set dashboard.create=false
        --set dnsServer.create=false)

for img in $(helm template cm chaos-mesh/chaos-mesh --version "$V" -n chaos-mesh "${VALUES[@]}" \
             | awk '$1 == "image:" {print $2}' | tr -d '"' | sort -u); do
  echo "image $img"
  docker pull -q --platform "linux/$ARCH" "$img" >/dev/null
  # Save only our platform: `kind load docker-image` imports multi-arch
  # images with --all-platforms and fails on the layers docker never pulled.
  tar=$(mktemp -t chaosimg)
  docker save --platform "linux/$ARCH" "$img" -o "$tar"
  kind load image-archive "$tar" --name "$CLUSTER" >/dev/null
  rm -f "$tar"
done

helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh --version "$V" \
  -n chaos-mesh --create-namespace "${VALUES[@]}" --wait --timeout 5m >/dev/null
kubectl -n chaos-mesh rollout status deploy/chaos-controller-manager --timeout=180s
kubectl -n chaos-mesh rollout status ds/chaos-daemon --timeout=180s
kubectl -n chaos-mesh get pods
