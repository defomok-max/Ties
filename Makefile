# Ties — terminal AI coding agent. Pure Go stdlib, single binary.

BINARY      := ties
PKG         := ./cmd/ties
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := github.com/defomok-max/Ties/internal/version
LDFLAGS     := -s -w \
	-X '$(VERSION_PKG).Version=$(VERSION)' \
	-X '$(VERSION_PKG).Commit=$(COMMIT)' \
	-X '$(VERSION_PKG).Date=$(DATE)'

# Install location. Override with: make install PREFIX=$HOME/.local
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build the ties binary into ./ties
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "Built ./$(BINARY) ($(VERSION))"

.PHONY: install
install: ## Build and install ties onto your PATH (uses sudo if needed)
	@CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@mkdir -p "$(BINDIR)" 2>/dev/null || true
	@if [ -w "$(BINDIR)" ]; then \
		install -m 0755 $(BINARY) "$(BINDIR)/$(BINARY)"; \
	else \
		echo "→ $(BINDIR) needs elevated permissions, using sudo"; \
		sudo install -m 0755 $(BINARY) "$(BINDIR)/$(BINARY)"; \
	fi
	@echo "Installed $(BINARY) → $(BINDIR)/$(BINARY)"
	@echo "Run: $(BINARY) --help"

.PHONY: uninstall
uninstall: ## Remove an installed ties binary
	@rm -f "$(BINDIR)/$(BINARY)" 2>/dev/null || sudo rm -f "$(BINDIR)/$(BINARY)"
	@echo "Removed $(BINDIR)/$(BINARY)"

.PHONY: test
test: ## Run the test suite
	go test ./...

.PHONY: race
race: ## Run the race detector on concurrent packages
	go test -race ./internal/tui/ ./internal/provider/...

.PHONY: lint
lint: ## Run gofmt, go vet and golangci-lint (if installed)
	@test -z "$$(gofmt -l .)" || (echo "gofmt: files need formatting:" && gofmt -l . && exit 1)
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

.PHONY: tidy
tidy: ## Verify the module is tidy (no external deps expected)
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY) *.test *.out

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
