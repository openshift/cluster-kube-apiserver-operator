#!/bin/bash
set -e
set -u
set -o pipefail

cd $( readlink -f "$( dirname "${0}" )/.." )

TMP_DIR=$( mktemp -d )

function cleanup() {
    return_code=$?
    rm -rf "${TMP_DIR}"
    exit "${return_code}"
}
trap "cleanup" EXIT


OUTDIR=${TMP_DIR} ./hack/update-generated-bindata.sh
diff -Naup {.,${TMP_DIR}}/pkg/operator/v311_00_assets/bindata.go
