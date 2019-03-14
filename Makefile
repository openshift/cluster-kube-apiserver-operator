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
$(call add-bindata,v3.11.0,./bindata/v3.11.0/...,bindata,v311_00_assets,pkg/operator/v311_00_assets/bindata.go)


clean:
	$(RM) ./cluster-kube-apiserver-operator
.PHONY: clean

# TODO: move this to library-go converted to `oc adm release new` which handles the substitution.
# (There are still some rough edges with that command.)
, := ,
IMAGES ?= cluster-kube-apiserver-operator
QUOTED_IMAGES=\"$(subst $(,),\"$(,)\",$(IMAGES))\"

origin-release:
	docker pull registry.svc.ci.openshift.org/openshift/origin-release:v4.0
	imagebuilder -file Dockerfile-origin-release --build-arg "IMAGE_ORG=$(IMAGE_ORG)" --build-arg "IMAGES=$(QUOTED_IMAGES)" -t "$(IMAGE_ORG)/origin-release:latest" hack
	@echo
	@echo "To install:"
	@echo
	@echo "  IMAGE_ORG=$(IMAGE_ORG) make images"
	@echo "  docker push $(IMAGE_ORG)/origin-release:latest"
	@echo "  docker push $(IMAGE_ORG)/origin-cluster-kube-apiserver-operator"
	@echo "  OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE=$(IMAGE_ORG)/origin-release:latest bin/openshift-install cluster --log-level=debug"

GO_TEST_PACKAGES :=./pkg/... ./cmd/...

.PHONY: test-e2e
test-e2e: GO_TEST_PACKAGES :=./test/e2e/...
test-e2e: test-unit

update-codegen-crds:
	go run ./vendor/github.com/openshift/library-go/cmd/crd-schema-gen/main.go --apis-dir vendor/github.com/openshift/api
update-codegen: update-codegen-crds
verify-codegen-crds:
	go run ./vendor/github.com/openshift/library-go/cmd/crd-schema-gen/main.go --apis-dir vendor/github.com/openshift/api --verify-only
verify-codegen: verify-codegen-crds
verify: verify-codegen
.PHONY: update-codegen-crds update-codegen verify-codegen-crds verify-codegen verify
