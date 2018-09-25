#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

cd $( readlink -f "$( dirname "${0}" )/.." )

VERIFY=--verify-only ./hack/update-codegen.sh
