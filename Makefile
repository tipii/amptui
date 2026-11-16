BINARY := amptui
PKG    := ./cmd/amptui

# The version --version prints and the servers are told: the nearest tag
# plus commits since it (v0.3.0-4-gabc1234-dirty). Release builds stamp it
# the same way, through GoReleaser.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS := -X github.com/tipii/amptui/internal/version.Version=$(VERSION)

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

.PHONY: build run install uninstall test vet tidy notices snapshot clean help

build: ## Build the amptui binary
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

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

notices: ## Write THIRD_PARTY_NOTICES (the dependencies' licenses)
	go run ./scripts/notices > THIRD_PARTY_NOTICES

snapshot: ## Build the release archives and packages locally into dist/ (needs goreleaser)
	goreleaser release --snapshot --clean

clean: ## Remove the built binary and release output
	rm -rf $(BINARY) dist THIRD_PARTY_NOTICES

help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
