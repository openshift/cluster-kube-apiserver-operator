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
TESTS_EXT_DIR := ./cmd/cluster-kube-apiserver-operator-tests
TESTS_EXT_OUTPUT_DIR := ./cmd/cluster-kube-apiserver-operator-tests

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
# Ensure test binary has correct name and location
# -------------------------------------------------------------------
.PHONY: tests-ext-build
tests-ext-build: build
	@mkdir -p $(TESTS_EXT_OUTPUT_DIR)
	@if [ -f cluster-kube-apiserver-operator-tests ] && [ ! -f $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY) ]; then \
		mv cluster-kube-apiserver-operator-tests $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY); \
	fi

# -------------------------------------------------------------------
# Run test suite
# -------------------------------------------------------------------
.PHONY: run-suite
run-suite: tests-ext-build
	@if [ -z "$(SUITE)" ]; then \
		echo "Error: SUITE variable is required. Usage: make run-suite SUITE=<suite-name> [JUNIT_DIR=<dir>]"; \
		exit 1; \
	fi
	@JUNIT_ARG=""; \
	if [ -n "$(JUNIT_DIR)" ]; then \
		mkdir -p $(JUNIT_DIR); \
		JUNIT_ARG="--junit-path=$(JUNIT_DIR)/junit.xml"; \
	fi; \
	$(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY) run-suite $(SUITE) $$JUNIT_ARG

clean:
	$(RM) ./cluster-kube-apiserver-operator
	rm -f $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY)
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
