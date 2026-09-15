PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: all build build-debug test fmt fmt-check lint staticcheck check install clean

all: build

build:
	@mkdir -p bin
	go build -ldflags "-s -w -X agentcli.local/ai/internal/cli.Version=0.1.0" -o bin/ai ./cmd/ai

build-debug:
	@mkdir -p bin
	go build -tags debug -ldflags "-X agentcli.local/ai/internal/cli.Version=0.1.0-debug" -o bin/ai-debug ./cmd/ai

test:
	go test -v ./...

fmt:
	go fmt ./...

fmt-check:
	@test -z "$$('$(shell go env GOROOT)/bin/gofmt' -l cmd internal)" || { echo "Run make fmt to format Go files."; exit 1; }

lint:
	go vet ./...

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

check: fmt-check lint test build build-debug
	@echo "All meta checks, tests, release and debug builds passed."

install:
	BINDIR="$(BINDIR)" scripts/build.sh --deploy

clean:
	rm -rf bin/
