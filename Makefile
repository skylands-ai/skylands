MODULE  := github.com/skylands-ai/skylands
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X $(MODULE)/internal/cli.version=$(VERSION)"

.PHONY: all build web test lint install

all: build

## web: build the browser client into web/dist, which the Go build embeds
web:
	cd web && npm ci && npm run build

## build: compile the skylands binary into bin/
build:
	go build $(LDFLAGS) -o bin/skylands ./cmd/skylands

## test: run every test
test:
	go test ./...

## lint: vet the code and run golangci-lint
lint:
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found; see https://golangci-lint.run/docs/welcome/install/"; exit 1; }
	golangci-lint run ./...

## install: install skylands into GOBIN, or GOPATH/bin when GOBIN is unset
install:
	go install $(LDFLAGS) ./cmd/skylands
