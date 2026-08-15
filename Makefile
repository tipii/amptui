BINARY := amptui
PKG    := ./cmd/amptui

# Where `make install` puts the binary. ~/.local/bin is the XDG location for
# user binaries and is already on PATH on most distros.
#
# This deliberately does NOT use `go install`: that obeys $GOBIN, and version
# managers (mise, asdf) point $GOBIN inside the *versioned* toolchain
# directory — e.g. ~/.local/share/mise/installs/go/1.26.6/bin. A binary
# installed there disappears from PATH the moment the Go version is bumped,
# silently leaving whatever older copy sits in ~/.local/bin to take over.
#
# Override for a system-wide install: make install BINDIR=/usr/local/bin
BINDIR ?= $(HOME)/.local/bin

.PHONY: build run install uninstall test vet tidy clean help

build: ## Build the amptui binary
	go build -o $(BINARY) $(PKG)

install: build ## Install amptui to $(BINDIR) (default ~/.local/bin)
	@mkdir -p $(BINDIR)
	install -m 0755 $(BINARY) $(BINDIR)/$(BINARY)
	@echo "installed $(BINDIR)/$(BINARY)"
	@command -v $(BINARY) >/dev/null || echo "warning: $(BINDIR) is not on your PATH"

uninstall: ## Remove the installed amptui binary (including legacy Go install paths)
	rm -f $(BINDIR)/$(BINARY)
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
