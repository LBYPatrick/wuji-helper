.DEFAULT_GOAL := help

export PATH := $(PATH):$(HOME)/.local/bin

.PHONY: help install format format-check test build build-cross run clean

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "─── Run ─────────────────────────────────────────────────────────"
	@echo "  install            Install missing Go and Wuji CLI using native sh installers"
	@echo "  run                Run the firmware TUI (requires Wuji CLI)"
	@echo "  build              Build bin/wuji-helper"
	@echo "  build-cross        Build Linux/macOS binaries for amd64 and arm64"
	@echo ""
	@echo "─── Development ─────────────────────────────────────────────────"
	@echo "  format             Format Go source files"
	@echo "  format-check       Check Go formatting without changing files"
	@echo "  test               Run race tests and check at least 70% coverage"
	@echo "  clean              Remove build output from bin/"
	@echo "  help               Show this help (default target)"

install:
	@sh scripts/install.sh

format:
	gofmt -w *.go

format-check:
	@files=$$(gofmt -l *.go) || exit 1; \
	if [ -n "$$files" ]; then printf 'Run make format for:\n%s\n' "$$files"; exit 1; fi

test:
	mkdir -p bin
	go test -race -timeout 60s -coverprofile=bin/coverage.out ./...
	@coverage=$$(go tool cover -func=bin/coverage.out) || exit 1; \
	printf '%s\n' "$$coverage"; \
	printf '%s\n' "$$coverage" | awk '/^total:/ { if ($$3 + 0 < 70) { print "Coverage must be at least 70%"; exit 1 } }'

build: install
	mkdir -p bin
	go build -o bin/wuji-helper .

build-cross: install
	@set -e; mkdir -p bin; \
	for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "Building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" go build -o "bin/wuji-helper-$$os-$$arch" .; \
	done

run: install
	go run .

clean:
	rm -rf bin
