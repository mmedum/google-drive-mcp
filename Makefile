# Common dev tasks. CI runs the same gates from .github/workflows/ci.yml.

GO       ?= go
BIN      ?= ./google-drive-mcp
VERSION  ?= dev
PKG       = github.com/mmedum/google-drive-mcp
LDFLAGS   = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
COVER_MIN ?= 80
# Tool versions are pinned: @latest means today's green build cannot be
# reproduced tomorrow. CI installs exactly these.
GOLANGCI_VERSION    ?= v2.13.2
GOVULNCHECK_VERSION ?= v1.7.0
GO_LICENSES_VERSION ?= v1.6.0
GOBIN    := $(shell $(GO) env GOPATH)/bin
# Prefer tools installed with the current Go (go install ...@latest) over distro packages.
export PATH := $(GOBIN):$(PATH)

.PHONY: all
all: check

.PHONY: build
build: ## Build the binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/google-drive-mcp

.PHONY: install
install: ## go install the binary
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags="$(LDFLAGS)" ./cmd/google-drive-mcp

.PHONY: fmt
fmt: ## Fail if gofmt would change anything
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt issues:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## go vet, including the integration-tagged tests so they keep compiling
	$(GO) vet ./...
	$(GO) vet -tags=integration ./...

.PHONY: lint
lint: ## golangci-lint, at the version CI pins
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run

.PHONY: test
test: ## Unit tests with race detector and coverage
	$(GO) test -race -coverpkg=./internal/... -coverprofile=cov.out -covermode=atomic ./...

.PHONY: cover
cover: test ## Enforce the coverage floor on core packages
	$(GO) run ./scripts/gates coverage cov.out $(COVER_MIN)

.PHONY: hooks
hooks: ## Install the git pre-commit hook
	$(GO) run ./scripts/gates install-hooks

.PHONY: integration
integration: ## Live tests against the signed-in account (writes nothing yet)
	GDRIVE_INTEGRATION=1 $(GO) test -tags=integration ./... -v -count=1

.PHONY: live
live: build ## Drive the binary against the signed-in Google account (redacted)
	$(GO) run ./scripts/livedrive -bin $(BIN) $(LIVE_ARGS)

.PHONY: evals
evals: build ## Agent evals against the signed-in account, in one scratch folder
	$(GO) run ./scripts/evals -bin $(BIN)

.PHONY: bench
bench: ## Benchmarks over the in-memory Drive
	$(GO) test -run XXX -bench . -benchmem ./internal/ref ./internal/render ./internal/service

.PHONY: vuln
vuln: ## Known vulnerabilities in anything we actually call
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: licenses
licenses: ## Dependencies must carry a licence we can redistribute under
	$(GO) run github.com/google/go-licenses@$(GO_LICENSES_VERSION) check ./... 		--allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC

.PHONY: schemas
schemas: build ## Dump tool schemas
	$(BIN) --dump-schemas > schemas.json

.PHONY: schema-diff
schema-diff: build ## Diff tool schemas against the last tag
	$(GO) run ./scripts/gates schema-diff $(BIN)

.PHONY: smoke
smoke: build ## Drive the binary over stdio
	$(GO) run ./scripts/gates smoke $(BIN)

.PHONY: leaks
leaks: ## Nothing from a real Drive may be in the repository
	$(GO) run ./scripts/gates leaks

.PHONY: staleness
staleness: build ## Docs must match the code
	$(GO) run ./scripts/gates staleness $(BIN)

.PHONY: pins
pins: ## Every tool a workflow installs must be one exact version
	$(GO) run ./scripts/gates pins

.PHONY: check
check: fmt vet lint cover vuln licenses leaks pins smoke staleness ## Everything CI runs

.PHONY: clean
clean:
	$(RM) $(BIN) cov.out schemas.json
