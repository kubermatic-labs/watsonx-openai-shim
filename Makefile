GORELEASER_VERSION = v2.18.2
GOLANGCI_LINT_VERSION = v2.14.0
BOILERPLATE_VERSION = v0.3.0

GORELEASER = _build/goreleaser
GOLANGCI_LINT = _build/golangci-lint
BOILERPLATE = _build/boilerplate


all: help

##@ Development

snapshot: $(GORELEASER) ## Dry-run the release (not pushing).
	$(GORELEASER) release --clean --fail-fast --snapshot

test: ## Run the unit tests.
	go test -cover ./...

lint: $(GOLANGCI_LINT) ## Run the linters.
	$(GOLANGCI_LINT) run ./...
	helm lint charts/watsonx-openai-shim --values=charts/watsonx-openai-shim/values.lint.yaml

clean: ## Delete build artifacts.
	rm -rf _build

verify-file-headers: $(BOILERPLATE) ## Verify license header within all files.
	$(BOILERPLATE) -boilerplates hack/boilerplate cmd internal charts Dockerfile

add-file-headers: ## Add license header to all files.
	./hack/add-license-header.sh cmd internal charts Dockerfile

##@ Release

release: $(GORELEASER) ## Perform the release (pushing).
	$(GORELEASER) release --clean --fail-fast

##@ General

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-19s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

$(GORELEASER):
	$(call go-install-tool,$(GORELEASER),github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION))

$(GOLANGCI_LINT):
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION))

$(BOILERPLATE):
	$(call go-install-tool,$(BOILERPLATE),github.com/kubermatic-labs/boilerplate@$(BOILERPLATE_VERSION))

# go-install-tool will 'go install' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PWD)/_build go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef
