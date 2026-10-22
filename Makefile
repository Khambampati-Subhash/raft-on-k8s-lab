SHELL := /bin/bash
BIN   := bin

.PHONY: test build
test:
	go test -race ./...

build:
	mkdir -p $(BIN)
	go build -o $(BIN)/ ./cmd/...
