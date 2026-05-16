BINARY := amptui
PKG    := ./cmd/amptui

.PHONY: build run test vet tidy clean help

build: ## Build the amptui binary
	go build -o $(BINARY) $(PKG)

run: ## Run amptui directly (no separate build step)
	go run $(PKG)

test: ## Run all tests
	go test ./...

vet: ## Run go vet on all packages
	go vet ./...

tidy: ## Run go mod tidy
	go mod tidy

clean: ## Remove the built binary
	rm -f $(BINARY)

help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
