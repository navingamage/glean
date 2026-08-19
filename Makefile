SHELL := /bin/bash
GO ?= go
BIN := bin
PKG := ./...

.PHONY: all
all: lint test build

.PHONY: build
build:
	@mkdir -p $(BIN)
	$(GO) build -trimpath -o $(BIN)/glean ./cmd/glean

.PHONY: test
test:
	$(GO) test -race -count=1 $(PKG)

.PHONY: cover
cover:
	$(GO) test -covermode=atomic -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -func=coverage.out | tail -n 1

.PHONY: lint
lint:
	$(GO) vet $(PKG)
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt: files need formatting"; exit 1)

# Probes every listing page in the registry and reports which still yield cases.
# Run it when a scan starts finding less than it used to.
.PHONY: check-sources
check-sources: build
	$(BIN)/glean sources check

.PHONY: clean
clean:
	rm -rf $(BIN) coverage.out
