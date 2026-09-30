.DEFAULT_GOAL := help

.PHONY: help format test build run clean

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "─── Run ─────────────────────────────────────────────────────────"
	@echo "  run                Run the firmware TUI (requires Wuji CLI)"
	@echo "  build              Build bin/wuji-firmware-bouncer"
	@echo ""
	@echo "─── Development ─────────────────────────────────────────────────"
	@echo "  format             Format Go source files"
	@echo "  test               Run tests with the race detector"
	@echo "  clean              Remove build output from bin/"
	@echo "  help               Show this help (default target)"

format:
	gofmt -w *.go

test:
	go test -race ./...

build:
	mkdir -p bin
	go build -o bin/wuji-firmware-bouncer .

run:
	go run .

clean:
	rm -rf bin
