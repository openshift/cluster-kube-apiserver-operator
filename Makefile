all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/library-go/alpha-build-machinery/make/, \
	golang.mk \
	targets/openshift/bindata.mk \
	targets/openshift/deps.mk \
	targets/openshift/images.mk \
)

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target suffix
# $2 - Dockerfile path
# $3 - context directory for image build
# It will generate target "image-$(1)" for builing the image an binding it as a prerequisite to target "images".
$(call build-image,origin-$(GO_PACKAGE),./Dockerfile,.)

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

CRD_SCHEMA_GEN_VERSION := v1.0.0
crd-schema-gen:
	git clone -b $(CRD_SCHEMA_GEN_VERSION) --single-branch --depth 1 https://github.com/openshift/crd-schema-gen.git $(CRD_SCHEMA_GEN_GOPATH)/src/github.com/openshift/crd-schema-gen
	GOPATH=$(CRD_SCHEMA_GEN_GOPATH) GOBIN=$(CRD_SCHEMA_GEN_GOPATH)/bin go install $(CRD_SCHEMA_GEN_GOPATH)/src/github.com/openshift/crd-schema-gen/cmd/crd-schema-gen

update-codegen-crds: CRD_SCHEMA_GEN_GOPATH :=$(shell mktemp -d)
update-codegen-crds: crd-schema-gen
	$(CRD_SCHEMA_GEN_GOPATH)/bin/crd-schema-gen --apis-dir vendor/github.com/openshift/api/operator/v1
update-codegen: update-codegen-crds

verify-codegen-crds: CRD_SCHEMA_GEN_GOPATH :=$(shell mktemp -d)
verify-codegen-crds: crd-schema-gen
	$(CRD_SCHEMA_GEN_GOPATH)/bin/crd-schema-gen --apis-dir vendor/github.com/openshift/api/operator/v1 --verify-only
verify-codegen: verify-codegen-crds
verify: verify-codegen
.PHONY: update-codegen-crds update-codegen verify-codegen-crds verify-codegen verify crd-schema-gen
