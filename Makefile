.PHONY: build install clean test lint

BINARY := spcli
BUILD_DIR := ./cmd/spcli

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags="-s -w -X github.com/soli0222/spotify-playlist-cli/internal/cli.Version=$(VERSION)" -o $(BINARY) $(BUILD_DIR)

install:
	go install -ldflags="-s -w -X github.com/soli0222/spotify-playlist-cli/internal/cli.Version=$(VERSION)" $(BUILD_DIR)

clean:
	rm -f $(BINARY)

test:
	go test ./...

lint:
	golangci-lint run ./...
