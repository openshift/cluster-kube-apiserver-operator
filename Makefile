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

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-cluster-kube-apiserver-operator,$(IMAGE_REGISTRY)/ocp/4.3:cluster-kube-apiserver-operator, ./Dockerfile.rhel7,.)

$(call verify-golang-versions,Dockerfile.rhel7)

clean:
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
