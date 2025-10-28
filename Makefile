all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/images.mk \
	targets/openshift/deps.mk \
	targets/openshift/operator/telepresence.mk \
)

# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/... ./cmd/...

IMAGE_REGISTRY :=registry.svc.ci.openshift.org

ENCRYPTION_PROVIDERS=aescbc aesgcm
ENCRYPTION_PROVIDER?=aescbc

TESTS_EXT_BINARY := cluster-kube-apiserver-operator-tests-ext
TESTS_EXT_DIR := ./test/extended/tests-extension
TESTS_EXT_PACKAGE := ./cmd

TESTS_EXT_GIT_COMMIT := $(shell git rev-parse --short HEAD)
TESTS_EXT_BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
TESTS_EXT_GIT_TREE_STATE := $(shell if git diff --quiet; then echo clean; else echo dirty; fi)

TESTS_EXT_LDFLAGS := -X 'github.com/openshift-eng/openshift-tests-extension/pkg/version.CommitFromGit=$(TESTS_EXT_GIT_COMMIT)' \
                     -X 'github.com/openshift-eng/openshift-tests-extension/pkg/version.BuildDate=$(TESTS_EXT_BUILD_DATE)' \
                     -X 'github.com/openshift-eng/openshift-tests-extension/pkg/version.GitTreeState=$(TESTS_EXT_GIT_TREE_STATE)'

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-cluster-kube-apiserver-operator,$(IMAGE_REGISTRY)/ocp/4.3:cluster-kube-apiserver-operator, ./Dockerfile.rhel7,.)

$(call verify-golang-versions,Dockerfile.rhel7)

TEST_E2E_ENCRYPTION_TARGETS=$(addprefix test-e2e-encryption-,$(ENCRYPTION_PROVIDERS))

# these are extremely slow serial e2e encryption tests that modify the cluster's global state
test-e2e-encryption: GO_TEST_PACKAGES :=./test/e2e-encryption/...
test-e2e-encryption: GO_TEST_FLAGS += -v
test-e2e-encryption: GO_TEST_FLAGS += -timeout 4h
test-e2e-encryption: GO_TEST_FLAGS += -p 1
test-e2e-encryption: GO_TEST_ARGS += -args -provider=$(ENCRYPTION_PROVIDER)
test-e2e-encryption: test-unit
.PHONY: test-e2e-encryption

.PHONY: $(TEST_E2E_ENCRYPTION_TARGETS)
$(TEST_E2E_ENCRYPTION_TARGETS): test-e2e-encryption-%:
	ENCRYPTION_PROVIDER=$* $(MAKE) test-e2e-encryption

TEST_E2E_ENCRYPTION_ROTATION_TARGETS=$(addprefix test-e2e-encryption-rotation-,$(ENCRYPTION_PROVIDERS))

# these are extremely slow serial e2e encryption rotation tests that modify the cluster's global state
test-e2e-encryption-rotation: GO_TEST_PACKAGES :=./test/e2e-encryption-rotation/...
test-e2e-encryption-rotation: GO_TEST_FLAGS += -v
test-e2e-encryption-rotation: GO_TEST_FLAGS += -timeout 4h
test-e2e-encryption-rotation: GO_TEST_FLAGS += -p 1
test-e2e-encryption-rotation: GO_TEST_ARGS += -args -provider=$(ENCRYPTION_PROVIDER)
test-e2e-encryption-rotation: test-unit
.PHONY: test-e2e-encryption-rotation

.PHONY: $(TEST_E2E_ENCRYPTION_ROTATION_TARGETS)
$(TEST_E2E_ENCRYPTION_ROTATION_TARGETS): test-e2e-encryption-rotation-%:
	ENCRYPTION_PROVIDER=$* $(MAKE) test-e2e-encryption-rotation

TEST_E2E_ENCRYPTION_PERF_TARGETS=$(addprefix test-e2e-encryption-perf-,$(ENCRYPTION_PROVIDERS))

test-e2e-encryption-perf: GO_TEST_PACKAGES :=./test/e2e-encryption-perf/...
test-e2e-encryption-perf: GO_TEST_FLAGS += -v
test-e2e-encryption-perf: GO_TEST_FLAGS += -timeout 2h
test-e2e-encryption-perf: GO_TEST_FLAGS += -p 1
test-e2e-encryption-perf: GO_TEST_ARGS += -args -provider=$(ENCRYPTION_PROVIDER)
test-e2e-encryption-perf: test-unit
.PHONY: test-e2e-encryption-perf

.PHONY: $(TEST_E2E_ENCRYPTION_PERF_TARGETS)
$(TEST_E2E_ENCRYPTION_PERF_TARGETS): test-e2e-encryption-perf-%:
	ENCRYPTION_PROVIDER=$* $(MAKE) test-e2e-encryption-perf

test-e2e: GO_TEST_PACKAGES :=./test/e2e/...
test-e2e: GO_TEST_FLAGS += -v
test-e2e: GO_TEST_FLAGS += -timeout 3h
test-e2e: GO_TEST_FLAGS += -p 1
test-e2e: test-unit
.PHONY: test-e2e

test-e2e-sno-disruptive: GO_TEST_PACKAGES :=./test/e2e-sno-disruptive/...
test-e2e-sno-disruptive: GO_TEST_FLAGS += -v
test-e2e-sno-disruptive: GO_TEST_FLAGS += -timeout 3h
test-e2e-sno-disruptive: GO_TEST_FLAGS += -p 1
test-e2e-sno-disruptive: test-unit
.PHONY: test-e2e-sno-disruptive

# -------------------------------------------------------------------
# Build binary with metadata (CI-compliant)
# -------------------------------------------------------------------
.PHONY: tests-ext-build
tests-ext-build:
	$(MAKE) -C test/extended/tests-extension build

# -------------------------------------------------------------------
# Run "update" and strip env-specific metadata
# -------------------------------------------------------------------
.PHONY: tests-ext-update
tests-ext-update:
	$(MAKE) -C test/extended/tests-extension build-update

# -------------------------------------------------------------------
# Clean test extension binaries
# -------------------------------------------------------------------
.PHONY: tests-ext-clean
tests-ext-clean:
	$(MAKE) -C $(TESTS_EXT_DIR) clean

# -------------------------------------------------------------------
# Run test suite
# -------------------------------------------------------------------
.PHONY: run-suite
run-suite:
	$(MAKE) -C $(TESTS_EXT_DIR) run-suite SUITE=$(SUITE) JUNIT_DIR=$(JUNIT_DIR)

# -------------------------------------------------------------------
# Run go test on ./test/extended/... with proper flags
# -------------------------------------------------------------------
.PHONY: test-extended
test-extended: GO_TEST_PACKAGES := ./test/extended/...
test-extended: GO_TEST_FLAGS += -v
test-extended: GO_TEST_FLAGS += -timeout 3h
test-extended: GO_TEST_FLAGS += -p 1
test-extended: test-unit
test-extended:
	GO111MODULE=on go test $(GO_TEST_FLAGS) $(GO_TEST_PACKAGES)

clean: tests-ext-clean
	$(RM) ./cluster-kube-apiserver-operator
.PHONY: clean

# Configure the 'telepresence' target
# See vendor/github.com/openshift/build-machinery-go/scripts/run-telepresence.sh for usage and configuration details
export TP_DEPLOYMENT_YAML ?=./manifests/0000_20_kube-apiserver-operator_06_deployment.yaml
export TP_CMD_PATH ?=./cmd/cluster-kube-apiserver-operator

# ensure the apirequestcounts crd is included in bindata
APIREQUESTCOUNT_CRD_TARGET := bindata/assets/kube-apiserver/apiserver.openshift.io_apirequestcount.yaml
APIREQUESTCOUNT_CRD_SOURCE := vendor/github.com/openshift/api/apiserver/v1/zz_generated.crd-manifests/kube-apiserver_apirequestcounts.crd.yaml
update-bindata-v4.1.0: $(APIREQUESTCOUNT_CRD_TARGET)
$(APIREQUESTCOUNT_CRD_TARGET): $(APIREQUESTCOUNT_CRD_SOURCE)
	cp $< $@

# ensure the correct version of the apirequestcounts crd is being used
verify-bindata-v4.1.0: verify-apirequestcounts-crd
.PHONY: verify-apirequestcounts-crd
verify-apirequestcounts-crd:
	diff -Naup $(APIREQUESTCOUNT_CRD_SOURCE) $(APIREQUESTCOUNT_CRD_TARGET)
