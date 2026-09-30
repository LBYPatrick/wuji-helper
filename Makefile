.PHONY: format test build run clean

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
