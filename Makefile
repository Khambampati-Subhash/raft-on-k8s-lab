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
