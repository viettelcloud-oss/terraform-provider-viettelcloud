# Variables
BINARY     := terraform-provider-viettelcloud
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -ldflags="-s -w -X main.version=$(VERSION)"
GOBIN      := $(shell go env GOPATH)/bin
GOEXE      := $(shell go env GOEXE)
MODULE     := github.com/viettelcloud-oss/terraform-provider-viettelcloud
SDK_MODULE := github.com/viettelcloud-oss/sdks/go
GO_SOURCES := main.go internal
TOOLS_DIR  := tools
DIST_DIR   := dist
# GOOS/GOARCH pairs that build-all cross-compiles.
PLATFORMS  ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# Default target
.PHONY: default
default: lint test build

# Update the SDK to its newest release tag, or pin one with
# make sdk-update SDK_VERSION=v0.1.0
# The SDK tag go/vX.Y.Z is the module version vX.Y.Z.
SDK_VERSION ?= latest

.PHONY: sdk-update
sdk-update:
	go get $(SDK_MODULE)@$(SDK_VERSION)
	go mod tidy
	@echo "SDK is now $$(go list -m -f '{{.Version}}' $(SDK_MODULE))"

# Build the provider binary for the host platform
.PHONY: build
build:
	go build $(LDFLAGS) -o $(BINARY)$(GOEXE) .

# Cross-compile the provider for every platform in PLATFORMS
.PHONY: build-all
build-all: $(addprefix build-,$(subst /,_,$(PLATFORMS)))

# Cross-compile one platform into dist/<os>_<arch>/, e.g. make build-windows_amd64.
# Pattern targets cannot be phony, so make would skip them if the file existed.
build-%:
	GOOS=$(word 1,$(subst _, ,$*)) GOARCH=$(word 2,$(subst _, ,$*)) CGO_ENABLED=0 \
		go build $(LDFLAGS) -o $(DIST_DIR)/$*/$(BINARY)$(if $(filter windows_%,$*),.exe) .

# Install into the local Terraform plugin directory for hand-testing
.PHONY: install
install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/viettelcloud-oss/viettelcloud/$(VERSION)/$(shell go env GOOS)_$(shell go env GOARCH)
	cp $(BINARY)$(GOEXE) ~/.terraform.d/plugins/registry.terraform.io/viettelcloud-oss/viettelcloud/$(VERSION)/$(shell go env GOOS)_$(shell go env GOARCH)/

# Unit tests
.PHONY: test
test:
	go test -v -cover -race -timeout 60s ./internal/...

# Unit-test statement coverage across all provider packages
.PHONY: coverage
coverage:
	go test -race -timeout 60s -covermode=atomic -coverpkg=./internal/... -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out

# Lint
.PHONY: lint
lint: $(GOBIN)/golangci-lint lint-file-structure
	$(GOBIN)/golangci-lint run ./...

# Framework entry points stay in contract-first order, and any file outside the
# standard three must document what it owns.
.PHONY: lint-file-structure
lint-file-structure:
	./tools/check_file_structure.sh

# Apply suggested fixes from the Go fix tool ahead of formatting.
.PHONY: fix
fix:
	go fix ./...

# Format
.PHONY: fmt
fmt: fix $(GOBIN)/gofumpt $(GOBIN)/goimports
	gofmt -s -w $(GO_SOURCES)
	$(GOBIN)/gofumpt -l -w $(GO_SOURCES)
	$(GOBIN)/goimports -w -local $(MODULE) $(GO_SOURCES)

# Regenerate provider docs
.PHONY: docs
docs: $(GOBIN)/tfplugindocs
	$(GOBIN)/tfplugindocs generate --provider-name viettelcloud

# Verify generated docs are up to date (CI check)
.PHONY: docs-check
docs-check: docs
	@git diff --exit-code docs/ || (echo "docs out of date — run 'make docs'"; exit 1)

# Install pinned developer tools.
.PHONY: tools
tools:
	cd $(TOOLS_DIR) && GOBIN="$(GOBIN)" go install tool

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -rf $(DIST_DIR)
	go clean -testcache

$(GOBIN)/golangci-lint $(GOBIN)/gofumpt $(GOBIN)/goimports $(GOBIN)/tfplugindocs:
	$(MAKE) tools
