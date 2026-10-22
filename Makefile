SHELL := /bin/bash
BIN   := bin
export GOTOOLCHAIN := local

.PHONY: test build
test:
	go test -race ./...

build:
	mkdir -p $(BIN)
	go build -o $(BIN)/ ./cmd/...
