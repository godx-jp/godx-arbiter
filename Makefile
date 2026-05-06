GO       := go
BIN      := bin/arbiter
PKG      := github.com/godx-team/godx-arbiter
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build test fmt vet lint tidy clean cross-compile run-pretool smoke

all: build

build:
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/arbiter

test:
	$(GO) test -race -count=1 ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

# lint runs golangci-lint if it's on PATH; otherwise falls back to vet
# so contributors without the tool still get something useful.
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed — running 'go vet' instead"; \
		$(GO) vet ./...; \
	fi

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin dist

cross-compile:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=dist/arbiter-$$os-$$arch$$ext; \
		echo ">> $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			$(GO) build -ldflags "$(LDFLAGS)" -o $$out ./cmd/arbiter || exit 1; \
	done
	@echo "✓ cross-compile done — dist/"

# Smoke: pipe a synthetic PreToolUse payload through the built binary.
smoke: build
	@echo '{"session_id":"smoke","cwd":"/tmp","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/etc/foo"}}' \
		| ./$(BIN) hook pretool

run-pretool: smoke
