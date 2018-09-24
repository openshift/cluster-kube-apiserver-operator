#!/bin/bash
set -e
set -u
set -o pipefail

cd $( readlink -f "$( dirname "${0}" )/.." )

# Setup temporary GOPATH so we can install go-bindata from vendor
export GOPATH=$( mktemp -d )
ln -s $( pwd )/vendor "${GOPATH}/src"
go install "./vendor/github.com/jteeuwen/go-bindata/..."

OUTDIR=${OUTDIR:-"."}
output="${OUTDIR}/pkg/operator/v311_00_assets/bindata.go"
${GOPATH}/bin/go-bindata \
    -nocompress \
    -nometadata \
    -prefix "manifests" \
    -pkg "v311_00_assets" \
    -o "${output}" \
    -ignore "OWNERS" \
    manifests/v3.11.0/...
gofmt -s -w "${output}"
