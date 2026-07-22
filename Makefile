.PHONY: build test install clean smoke-ha test-integration

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)

build:
	mkdir -p bin
	go build -ldflags "-X github.com/abyssmemes/contextverse/internal/version.Version=$(VERSION)" -o bin/contextd ./cmd/contextd

test:
	go test ./...

# MinIO + Postgres via docker-compose.backends.yml, then -tags=integration.
test-integration:
	bash ./scripts/test-integration.sh

install: build
	install -m 755 bin/contextd "$(HOME)/.local/bin/contextd"

clean:
	rm -rf bin

# Health readiness probe (does not stop the server). See docs: contextverse-server-upgrades.
smoke-ha:
	bash ./scripts/smoke-ha.sh "$(or $(LISTEN),127.0.0.1:8743)"
