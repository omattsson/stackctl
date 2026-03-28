# stackctl CLI Makefile

BINARY_NAME = stackctl
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

STATICCHECK_VERSION ?= 2026.1

# Detect Windows for .exe extension
ifdef GOOS
  ifeq ($(GOOS),windows)
    EXT = .exe
  endif
else
  ifeq ($(OS),Windows_NT)
    EXT = .exe
  endif
endif

# windows/arm64 excluded: not a common deployment target and avoids cross-compilation issues
PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build build-all test lint coverage install clean tools

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME)$(EXT) .

build-all:
	@mkdir -p bin
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		output=bin/$(BINARY_NAME)-$${os}-$${arch}; \
		if [ "$${os}" = "windows" ]; then output=$${output}.exe; fi; \
		echo "Building $${output}..."; \
		CGO_ENABLED=0 GOOS=$${os} GOARCH=$${arch} go build -ldflags "$(LDFLAGS)" -o $${output} . || exit 1; \
	done

test:
	go test ./... -v

lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck not installed, skipping (run 'make tools' to install staticcheck@$(STATICCHECK_VERSION))"; \
	fi

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

install:
	CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" .

tools:
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

clean:
	rm -rf bin/
