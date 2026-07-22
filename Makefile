.PHONY: build test install clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)

build:
	mkdir -p bin
	go build -ldflags "-X github.com/abyssmemes/contextverse/internal/version.Version=$(VERSION)" -o bin/contextd ./cmd/contextd

test:
	go test ./...

install: build
	install -m 755 bin/contextd "$(HOME)/.local/bin/contextd"

clean:
	rm -rf bin
