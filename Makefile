SHELL := /bin/sh

GO ?= go
BINARY := regru
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf '%s' none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/adinvadim/reg-ru-cli/internal/version.Version=$(VERSION) -X github.com/adinvadim/reg-ru-cli/internal/version.Commit=$(COMMIT) -X github.com/adinvadim/reg-ru-cli/internal/version.Date=$(BUILD_DATE)

.PHONY: build check dist fmt test test-race vet

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/regru

fmt:
	@files="$$(gofmt -l .)"; \
	if test -n "$$files"; then \
		printf '%s\n' "$$files"; \
		printf '%s\n' 'Go files need formatting; run gofmt -w on the files above.' >&2; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

check: fmt vet test test-race

dist:
	mkdir -p dist
	@set -eu; \
	for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; suffix=; \
		if test "$$os" = windows; then suffix=.exe; fi; \
		output="dist/$(BINARY)_$(VERSION)_$${os}_$${arch}$${suffix}"; \
		printf 'building %s\n' "$$output"; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$$output" ./cmd/regru; \
	done
	@cd dist && \
	if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum regru_$(VERSION)_* > checksums.txt; \
	else \
		shasum -a 256 regru_$(VERSION)_* > checksums.txt; \
	fi
