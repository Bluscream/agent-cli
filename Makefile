PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: all build build-debug test fmt lint check install clean

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

lint:
	go vet ./...

check: fmt lint test build build-debug
	@echo "All meta checks, tests, release and debug builds passed."

install: build
	@mkdir -p $(BINDIR)
	install -m 0755 bin/ai $(BINDIR)/ai
	@echo "Installed ai binary to $(BINDIR)/ai"

clean:
	rm -rf bin/
