all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/library-go/alpha-build-machinery/make/, \
	golang.mk \
	targets/openshift/bindata.mk \
	targets/openshift/deps.mk \
	targets/openshift/images.mk \
)

IMAGE_REGISTRY :=registry.svc.ci.openshift.org

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-cluster-kube-apiserver-operator,$(IMAGE_REGISTRY)/ocp/4.2:cluster-kube-apiserver-operator, ./Dockerfile.rhel7,.)

# This will call a macro called "add-bindata" which will generate bindata specific targets based on the parameters:
# $0 - macro name
# $1 - target suffix
# $2 - input dirs
# $3 - prefix
# $4 - pkg
# $5 - output
# It will generate targets {update,verify}-bindata-$(1) logically grouping them in unsuffixed versions of these targets
# and also hooked into {update,verify}-generated for broader integration.
$(call add-bindata,v4.1.0,./bindata/v4.1.0/...,bindata,v410_00_assets,pkg/operator/v410_00_assets/bindata.go)


clean:
	$(RM) ./cluster-kube-apiserver-operator
.PHONY: clean

GO_TEST_PACKAGES :=./pkg/... ./cmd/...

.PHONY: test-e2e
test-e2e: GO_TEST_PACKAGES :=./test/e2e/...
test-e2e: GO_TEST_FLAGS += -v
test-e2e: test-unit

CRD_SCHEMA_GEN_APIS := ./vendor/github.com/openshift/api/operator/v1
CRD_SCHEMA_GEN_VERSION := v0.2.1

crd-schema-gen:
	mkdir -p $(CRD_SCHEMA_GEN_TEMP)/bin
	git clone -b $(CRD_SCHEMA_GEN_VERSION) --single-branch --depth 1 https://github.com/kubernetes-sigs/controller-tools.git $(CRD_SCHEMA_GEN_TEMP)/gen
	cd $(CRD_SCHEMA_GEN_TEMP)/gen; GO111MODULE=on go build ./cmd/controller-gen; mv controller-gen ../bin
	curl -f -L -o $(CRD_SCHEMA_GEN_TEMP)/bin/yq https://github.com/mikefarah/yq/releases/download/2.4.0/yq_$(shell uname -s | tr A-Z a-z)_amd64 && chmod +x $(CRD_SCHEMA_GEN_TEMP)/bin/yq
update-codegen-crds: CRD_SCHEMA_GEN_TEMP :=$(shell mktemp -d)
update-codegen-crds: crd-schema-gen
	$(CRD_SCHEMA_GEN_TEMP)/bin/controller-gen schemapatch:manifests=./manifests output:dir=./manifests paths="$(subst $() $(),;,$(CRD_SCHEMA_GEN_APIS))"
	for p in manifests/*.crd.yaml-merge-patch; do $(CRD_SCHEMA_GEN_TEMP)/bin/yq m -i "$${p%%.crd.yaml-merge-patch}.crd.yaml" "$$p"; done
verify-codegen-crds: update-codegen-crds
	git diff -q manifests/ || { echo "Changed manifests: "; echo; git diff; false; }

update-codegen: update-codegen-crds
verify-codegen: verify-codegen-crds
verify: verify-codegen
.PHONY: update-codegen-crds update-codegen verify-codegen-crds verify-codegen verify crd-schema-gen
