# StackKits CLI Makefile (mise wrapper)

# Variables
BINARY_NAME=stackkit
VERSION?=dev

.PHONY: all build build-server build-all clean test test-unit test-integration test-cue test-cue-binding test-coverage lint fmt deps help

# Default target
all: deps lint test build

build: ## Build the CLI binary
	mise run build

build-server: ## Build the API server binary
	go build -ldflags "-X main.Version=$(VERSION)" -o build/stackkit-server ./cmd/stackkit-server

build-all: build build-server build-linux build-darwin build-windows ## Build CLI, server, and all platforms

build-linux: ## Build for Linux
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o build/$(BINARY_NAME)-linux-amd64 ./cmd/stackkit
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(VERSION)" -o build/$(BINARY_NAME)-linux-arm64 ./cmd/stackkit

build-darwin: ## Build for macOS
	GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o build/$(BINARY_NAME)-darwin-amd64 ./cmd/stackkit
	GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.Version=$(VERSION)" -o build/$(BINARY_NAME)-darwin-arm64 ./cmd/stackkit

build-windows: ## Build for Windows
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o build/$(BINARY_NAME)-windows-amd64.exe ./cmd/stackkit

install: build ## Install CLI to GOPATH/bin
	mise run install

clean: ## Remove build artifacts
	mise run clean

deps: ## Download dependencies
	mise run deps

test: ## Run all tests
	mise run test

test-unit: ## Run unit tests
	mise run test-unit

test-cue: ## Run CUE schema validation
	mise run test-cue

test-cue-binding: ## Validate CUE module contracts and Go binding pipeline
	cue vet -c=false ./modules/...
	go test ./internal/cue ./internal/composition ./cmd/stackkit/commands

test-validation: ## Run 3-layer validation suite
	mise run test-validation

test-coverage: ## Run tests with coverage
	mise run test-coverage

test-base-kit: ## Run base-kit tests
	cd base-kit && ./tests/run_tests.sh

test-dev-homelab: ## Run dev-homelab validation
	cue vet ./dev-homelab/...

test-e2e-dev-homelab: ## Run dev-homelab E2E (requires Docker)
	cd dev-homelab && ./tests/e2e_test.sh

test-e2e: test-e2e-dev-homelab ## Run full E2E suite

lint: ## Run linter
	mise run lint

fmt: ## Format code
	mise run fmt

run: ## Run CLI in dev mode (usage: make run ARGS="...")
	go run ./cmd/stackkit $(ARGS)

help: ## Show this help
	@echo "StackKits CLI"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
