GOFLAGS :=

all build:
	go build $(GOFLAGS) ./cmd/cluster-kube-apiserver-operator
.PHONY: all build

verify-govet:
	go vet $(GOFLAGS) ./...
.PHONY: verify-govet

verify: verify-govet
	hack/verify-gofmt.sh
	hack/verify-codegen.sh
	hack/verify-generated-bindata.sh
.PHONY: verify

test test-unit:
ifndef JUNITFILE
	go test $(GOFLAGS) -race ./...
else
ifeq (, $(shell which gotest2junit 2>/dev/null))
$(error gotest2junit not found! Get it by `go get -u github.com/openshift/release/tools/gotest2junit`.)
endif
	go test $(GOFLAGS) -race -json ./... | gotest2junit > $(JUNITFILE)
endif
.PHONY: test-unit

images:
	imagebuilder -f Dockerfile -t openshift/origin-cluster-kube-apiserver-operator .
.PHONY: images

clean:
	$(RM) ./cluster-kube-apiserver-operator
.PHONY: clean
