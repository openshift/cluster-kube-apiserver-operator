all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/images.mk \
	targets/openshift/crd-schema-gen.mk \
	targets/openshift/deps.mk \
	targets/openshift/operator/telepresence.mk \
)

# Set crd-schema-gen variables
CONTROLLER_GEN_VERSION :=v0.2.1
CRD_APIS :=./vendor/github.com/openshift/api/operator/v1

# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/... ./cmd/...

IMAGE_REGISTRY :=registry.svc.ci.openshift.org

ENCRYPTION_PROVIDERS=aescbc aesgcm
ENCRYPTION_PROVIDER?=aescbc

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-cluster-kube-apiserver-operator,$(IMAGE_REGISTRY)/ocp/4.3:cluster-kube-apiserver-operator, ./Dockerfile.rhel7,.)

# This will call a macro called "add-crd-gen" will will generate crd manifests based on the parameters:
# $1 - target name
# $2 - apis
# $3 - manifests
# $4 - output
$(call add-crd-gen,manifests,$(CRD_APIS),./manifests,./manifests)

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

update-codegen: update-codegen-crds
.PHONY: update-codegen

verify-codegen: verify-codegen-crds
.PHONY: verify-codegen

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

clean:
	$(RM) ./cluster-kube-apiserver-operator
.PHONY: clean

# Configure the 'telepresence' target
# See vendor/github.com/openshift/build-machinery-go/scripts/run-telepresence.sh for usage and configuration details
export TP_DEPLOYMENT_YAML ?=./manifests/0000_20_kube-apiserver-operator_06_deployment.yaml
export TP_CMD_PATH ?=./cmd/cluster-kube-apiserver-operator

# ensure the apirequestcounts crd is included in bindata
APIREQUESTCOUNT_CRD_TARGET := bindata/assets/kube-apiserver/apiserver.openshift.io_apirequestcount.yaml
APIREQUESTCOUNT_CRD_SOURCE := vendor/github.com/openshift/api/apiserver/v1/apiserver.openshift.io_apirequestcount.yaml
update-bindata-v4.1.0: $(APIREQUESTCOUNT_CRD_TARGET)
$(APIREQUESTCOUNT_CRD_TARGET): $(APIREQUESTCOUNT_CRD_SOURCE)
	cp $< $@

# ensure the correct version of the apirequestcounts crd is being used
verify-bindata-v4.1.0: verify-apirequestcounts-crd
.PHONY: verify-apirequestcounts-crd
verify-apirequestcounts-crd:
	diff -Naup $(APIREQUESTCOUNT_CRD_SOURCE) $(APIREQUESTCOUNT_CRD_TARGET)
