# Get the directory where this Makefile is, so we can use it below for including
DIR := $(strip $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST)))))

# Reference parent repo's build-machinery-go (no need to vendor it in test module)
REPO_ROOT := $(shell git rev-parse --show-toplevel)
include $(addprefix $(REPO_ROOT)/vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/deps.mk \
)

# Definitions for the extended tests
GO_PKG_NAME := github.com/openshift-eng/openshift-tests-extension

GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo 'unknown')
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_TREE_STATE := $(shell if git rev-parse --git-dir > /dev/null 2>&1; then if git diff --quiet; then echo clean; else echo dirty; fi; else echo unknown; fi)

LDFLAGS := -X '$(GO_PKG_NAME)/pkg/version.CommitFromGit=$(GIT_COMMIT)' \
           -X '$(GO_PKG_NAME)/pkg/version.BuildDate=$(BUILD_DATE)' \
           -X '$(GO_PKG_NAME)/pkg/version.GitTreeState=$(GIT_TREE_STATE)'

METADATA := $(shell pwd)/.openshift-tests-extension

TOOLS_BIN_DIR := $(CURDIR)/bin
BINARY_NAME := cluster-kube-apiserver-operator-tests-ext

.PHONY: help
help: #HELP Display essential help.
	@awk 'BEGIN {FS = ":[^#]*#HELP"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\n"} /^[a-zA-Z_0-9-]+:.*#HELP / { printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2 } ' $(MAKEFILE_LIST)

#SECTION Development
# Note: verify target is extended from build-machinery-go's golang.mk
# which provides verify-gofmt and verify-govet
.PHONY: verify #HELP To verify the code
verify: verify-deps

.PHONY: tidy #HELP Run go mod tidy and vendor.
tidy:
	go mod tidy
	go mod vendor

.PHONY: build
build: #HELP Build the extended tests binary
	@mkdir -p $(TOOLS_BIN_DIR)
	GO_COMPLIANCE_POLICY="exempt_all" CGO_ENABLED=0 go build -mod=vendor -ldflags "$(LDFLAGS)" -o $(TOOLS_BIN_DIR)/$(BINARY_NAME) ./cmd/...

.PHONY: update-metadata
update-metadata: build #HELP Build and run 'update-metadata' to generate test metadata
	$(TOOLS_BIN_DIR)/$(BINARY_NAME) update
	$(MAKE) clean-metadata

.PHONY: build-update
build-update: build update-metadata #HELP Build and update metadata and sanitize output

.PHONY: clean
clean: #HELP Remove build artifacts
	rm -rf $(TOOLS_BIN_DIR)

#SECTION Metadata

.PHONY: list-test-names
list-test-names: build #HELP Show current full test names
	@$(TOOLS_BIN_DIR)/$(BINARY_NAME) list -o names

.PHONY: run-suite
run-suite: build #HELP Run a test suite (usage: make run-suite SUITE=<suite-name> [JUNIT_DIR=<dir>])
	@if [ -z "$(SUITE)" ]; then \
		echo "Error: SUITE variable is required. Usage: make run-suite SUITE=<suite-name> [JUNIT_DIR=<dir>]"; \
		exit 1; \
	fi
	@JUNIT_ARG=""; \
	if [ -n "$(JUNIT_DIR)" ]; then \
		mkdir -p $(JUNIT_DIR); \
		JUNIT_ARG="--junit-path=$(JUNIT_DIR)/junit.xml"; \
	fi; \
	$(TOOLS_BIN_DIR)/$(BINARY_NAME) run-suite $(SUITE) $$JUNIT_ARG

.PHONY: clean-metadata
clean-metadata: #HELP Remove 'codeLocations' from metadata JSON
	@echo "Cleaning metadata (removing codeLocations)..."
	@for f in $(METADATA)/*.json; do \
		jq 'map(del(.codeLocations))' "$$f" > "$$f.tmp" && mv "$$f.tmp" "$$f"; \
	done

.PHONY: verify-metadata #HELP To verify that the metadata was properly updated
verify-metadata: update-metadata
	@if ! git diff --exit-code $(METADATA); then \
		echo "ERROR: Metadata is out of date. Please run 'make build-update' and commit the result."; \
		exit 1; \
	fi
