# Common dev tasks. CI runs the same gates from .github/workflows/ci.yml.

GO       ?= go
BIN      ?= ./google-drive-mcp
VERSION  ?= dev
PKG       = github.com/mmedum/google-drive-mcp/v3
LDFLAGS   = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
COVER_MIN ?= 80
# Tool versions are pinned: @latest means today's green build cannot be
# reproduced tomorrow. CI runs exactly these.
GOLANGCI_LINT ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
GOVULNCHECK   ?= golang.org/x/vuln/cmd/govulncheck@v1.7.0
GOLICENSES    ?= github.com/google/go-licenses@v1.6.0
# The module path is zricethezav, not gitleaks: the project moved
# organization and the module path did not follow it.
GITLEAKS      ?= github.com/zricethezav/gitleaks/v8@v8.30.1
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
	@out=$$(gofmt -l .); [ -z "$$out" ] || { echo "$$out"; exit 1; }

.PHONY: vet
vet: ## go vet, including the integration-tagged tests so they keep compiling
	$(GO) vet ./...
	$(GO) vet -tags=integration ./...

.PHONY: lint
lint: ## golangci-lint, at the version CI pins
	$(GO) run $(GOLANGCI_LINT) run

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
integration: ## Read-only live tests against the signed-in account
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
	$(GO) run $(GOVULNCHECK) ./...

.PHONY: licenses
licenses: ## Dependencies must carry a license we can redistribute under
	$(GO) run $(GOLICENSES) check ./... 		--allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC

# gitleaks walks every commit the clone holds, as `git log --all` lists
# them (https://github.com/gitleaks/gitleaks/blob/v8.30.1/sources/git.go#L93),
# so CI checks out the whole history for it.
.PHONY: gitleaks
gitleaks: ## No credential in any commit, at the gitleaks version pinned here
	$(GO) run $(GITLEAKS) git . --config .gitleaks.toml --redact --no-banner

.PHONY: schemas
schemas: build ## Dump tool schemas
	$(BIN) --dump-schemas > schemas.json

.PHONY: schema-diff
schema-diff: build ## Diff tool schemas against the newest release's recorded baseline
	$(GO) run ./scripts/gates schema-diff $(BIN)

# A pull request's commits name each item of the tool surface they change.
# CI runs it on every pull request; it is not in `check`, which has no
# pull request to measure from. Locally: make schema-ack PR_BASE_REF=origin/main
PR_BASE_REF ?= origin/main
PR_HEAD     ?= HEAD

.PHONY: schema-ack
schema-ack: ## Commit footers name each tool-surface change since the merge-base with PR_BASE_REF
	$(GO) run ./scripts/gates schema-ack $(PR_BASE_REF) $(PR_HEAD)

.PHONY: schema-baseline
schema-baseline: build ## Record the release being cut as the baseline: VERSION=vX.Y.Z, in its release commit
	$(GO) run ./scripts/gates schema-baseline $(BIN)

.PHONY: smoke
smoke: build ## Drive the binary over stdio
	$(GO) run ./scripts/gates smoke $(BIN)

.PHONY: leaks
leaks: ## Nothing from a real Drive may be in the repository
	$(GO) run ./scripts/gates leaks

.PHONY: staleness
staleness: build ## Docs must match the code
	$(GO) run ./scripts/gates staleness $(BIN)

.PHONY: docs
docs: ## Every decision, citation, link, path and live step the docs and code name resolves
	$(GO) run ./scripts/gates docs

.PHONY: pins
pins: ## Every tool a workflow installs must be one exact version
	$(GO) run ./scripts/gates pins

.PHONY: classes
classes: ## The error classes the code emits must be the ones it declares
	$(GO) run ./scripts/gates classes

.PHONY: api-coverage
api-coverage: ## Every API method is used on purpose or left out on purpose
	$(GO) run ./scripts/gates api-coverage

.PHONY: api-fields
api-fields: ## Every published field is modeled on purpose or left out on purpose
	$(GO) run ./scripts/gates api-fields

.PHONY: mcpb
mcpb: ## The committed bundle manifest must name files the packer will stage
	$(GO) run ./scripts/gates mcpb

.PHONY: transcript
transcript: ## A program driving a real account may only print through the redactor
	$(GO) run ./scripts/gates transcript

.PHONY: live-cover
live-cover: build ## Every tool option is driven live or recorded as not, with the reason
	$(GO) run ./scripts/gates live-cover $(BIN)

.PHONY: outcomes
outcomes: ## No result sentence may be written from the request rather than the response
	$(GO) run ./scripts/gates outcomes

.PHONY: registry
registry: ## The committed MCP registry entry must obey the registry's own rules
	$(GO) run ./scripts/gates registry

.PHONY: parity
parity: ## `make check` and CI must run the same gates
	$(GO) run ./scripts/gates parity

.PHONY: mcpb-manifest
mcpb-manifest: ## Print the bundle manifest stamped as the packer stamps it: VERSION=X.Y.Z
	$(GO) run ./scripts/gates mcpb-manifest $(VERSION)

.PHONY: api-diff
api-diff: ## Refetch the three discovery documents and report what has changed (needs the network)
	$(GO) run ./scripts/gates api-diff

# The half a vendored schema cannot do for itself: a digest says these
# bytes are the ones somebody reviewed, not that upstream still serves
# them. Manual, and read at release time.
.PHONY: schema-refetch
schema-refetch: ## Check the vendored schemas against what their sources serve (needs the network)
	$(GO) run ./scripts/gates schema-refetch

.PHONY: check
check: fmt vet lint cover vuln licenses gitleaks leaks pins classes api-coverage api-fields mcpb transcript schema-diff smoke staleness docs live-cover outcomes registry parity ## Everything CI runs

.PHONY: clean
clean:
	$(RM) $(BIN) cov.out schemas.json
