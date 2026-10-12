# MList — build helpers. Run from the server/ directory.
#
#   make all     build the web UI and the server binary (bin/mlist)
#   make run     run the server (uses ./config.yaml when present)
#   make dist    cross-compile release binaries into bin/dist/

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := bin/mlist
WEB_DIR := ../web
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 windows/amd64

GO ?= go
CONFIG := $(if $(wildcard config.yaml),-config config.yaml,)

.PHONY: all web build test vet run dist docs clean

all: web build

web:
	cd $(WEB_DIR) && pnpm install --frozen-lockfile && pnpm build

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mlist

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

# 由 Go 注解重新生成 OpenAPI 规范（server/internal/api/openapi.json）。
# openapi.json 是生成物，禁止手写；CI 会用 scripts/check-openapi.sh 校验。
docs:
	./scripts/gen-openapi.sh

run: build
	./$(BIN) $(CONFIG)

# The SQLite driver is pure Go, so CGO is not needed for any target.
dist:
	@mkdir -p bin/dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=bin/dist/mlist-$(VERSION)-$$os-$$arch$$ext; \
		echo "==> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd/mlist; \
	done

clean:
	rm -rf bin
