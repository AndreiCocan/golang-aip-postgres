# Pinned tool versions.
GOLANGCI_LINT_VERSION := v2.13.2

.PHONY: tools lint lint-go lint-go-fix test test-short build fmt help

## tools: Install pinned development tools (golangci-lint)
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

## lint: Run all linters
lint: lint-go

## lint-go: Run Go linters
lint-go:
	@echo "Running golang linter..."
	go vet ./...
	golangci-lint run ./...

## lint-go-fix: Run Go linter and auto-fix issues when possible
lint-go-fix:
	@echo "Running golang linter with auto-fix..."
	golangci-lint run --fix ./...

## test: Run Go unit and execution tests (execution tests start PostgreSQL in Docker)
test:
	@echo "Running tests..."
	go test ./... -coverprofile=coverage.out -covermode=atomic

## test-short: Run Go unit tests only, without Docker
test-short:
	@echo "Running unit tests..."
	go test -short ./...

## build: Build all Go packages
build:
	@echo "Building Go packages..."
	go build ./...

## fmt: Format Go code
fmt:
	@echo "Running formatter..."
	golangci-lint fmt ./...

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
