BINARY := amptui
PKG    := ./cmd/amptui

.PHONY: build run install uninstall test vet tidy clean help

build: ## Build the amptui binary
	go build -o $(BINARY) $(PKG)

install: ## Install amptui to $GOBIN (or $GOPATH/bin) so it's on PATH
	go install $(PKG)

uninstall: ## Remove the installed amptui binary from $GOBIN / $GOPATH/bin
	rm -f $$(go env GOBIN)/$(BINARY) $$(go env GOPATH)/bin/$(BINARY)

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
