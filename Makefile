SHELL := /bin/bash
BIN   := bin
export GOTOOLCHAIN := local

.PHONY: test build
test:
	go test -race ./...

build:
	mkdir -p $(BIN)
	go build -o $(BIN)/ ./cmd/...

.PHONY: local-up local-down
local-up:
	./scripts/local-up.sh

local-down:
	./scripts/local-down.sh

# ---- Kubernetes (kind) ----
CLUSTER := raftlab
IMAGE   ?= raftlab:$(shell git describe --always --dirty)
# Docker Desktop set to "8 GB" reports ~7.8 GiB (kernel reserve), so check 7.5 GiB.
MIN_DOCKER_MEM := 8053063680

.PHONY: tools image cluster-up cluster-up-5 cluster-down load-image
tools:
	@for t in kind helm kubectl docker; do command -v $$t >/dev/null || { echo "missing: $$t"; exit 1; }; done
	@mem=$$(docker info --format '{{.MemTotal}}') || { echo "docker daemon not reachable"; exit 1; }; \
	  echo "docker memory: $$((mem / 1048576)) MiB"; \
	  [ "$$mem" -ge $(MIN_DOCKER_MEM) ] || { echo "need >= 7.5 GiB for docker (set Docker Desktop to 8 GB+)"; exit 1; }

ARCH ?= arm64

image:
	rm -rf $(BIN)/linux && mkdir -p $(BIN)/linux
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -o $(BIN)/linux/ ./cmd/...
	docker build --platform linux/$(ARCH) -t $(IMAGE) .

cluster-up: tools
	kind create cluster --config deploy/kind/cluster-3.yaml --wait 120s
	kubectl wait --for=condition=Ready nodes --all --timeout=120s

cluster-up-5: tools
	kind create cluster --config deploy/kind/cluster-5.yaml --wait 120s
	kubectl wait --for=condition=Ready nodes --all --timeout=120s

cluster-down:
	kind delete cluster --name $(CLUSTER)

load-image: image
	kind load docker-image $(IMAGE) --name $(CLUSTER)

.PHONY: stage3-deploy stage3-demo
stage3-deploy: load-image
	kubectl kustomize deploy/stage3-deployment | sed "s|image: raftlab:dev|image: $(IMAGE)|" | kubectl apply -f -

stage3-demo: load-image
	IMAGE=$(IMAGE) ./scripts/stage3-demo.sh

.PHONY: deploy undeploy smoke watch
deploy: load-image
	kubectl kustomize deploy/base | sed "s|image: raftlab:dev|image: $(IMAGE)|" | kubectl apply -f -

undeploy:
	kubectl delete namespace raftlab --ignore-not-found

smoke:
	./scripts/smoke.sh

watch:
	kubectl -n raftlab exec -it deploy/raftwatch -- /raftwatch --service kv.raftlab.svc.cluster.local --port 8080

# ---- Chaos ----
CHAOS_MESH_VERSION := 2.8.6

.PHONY: chaos-install
chaos-install:
	CHAOS_MESH_VERSION=$(CHAOS_MESH_VERSION) ARCH=$(ARCH) CLUSTER=$(CLUSTER) ./scripts/chaos-install.sh

exp-%:
	./scripts/exp.sh $*
