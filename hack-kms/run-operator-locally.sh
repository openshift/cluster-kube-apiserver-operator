#!/bin/bash

# # From pod spec
# args:
# - --config=/var/run/configmaps/config/config.yaml
# command:
# - cluster-kube-apiserver-operator
# - operator
# env:
# - name: IMAGE
# value: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:3c8904c40249c175fae0f095b690c4c2af17e894b521fa4cab143b0b6d2ce5bd
# - name: OPERATOR_IMAGE
# value: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:7270ceb168750f0c4ae0afb0086b6dc111dd0da5a96ef32638e8c414b288d228
# - name: OPERAND_IMAGE_VERSION
# value: 1.26.5
# - name: OPERATOR_IMAGE_VERSION
# value: 4.13.3
# - name: POD_NAME
# valueFrom:
#     fieldRef:
#     apiVersion: v1
#     fieldPath: metadata.name

# export IMAGE=quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:3c8904c40249c175fae0f095b690c4c2af17e894b521fa4cab143b0b6d2ce5bd
export IMAGE=quay.io/okd/scos-content@sha256:838ce7c903c261457e5e9061233efebc1adece37ea2b61f44c0601bb4d1843da
# export OPERATOR_IMAGE=quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:7270ceb168750f0c4ae0afb0086b6dc111dd0da5a96ef32638e8c414b288d228
export OPERATOR_IMAGE=quay.io/okd/scos-content@sha256:4ec2d6b07f3f6681ee3d174e54def3fd3a5849a6126b539e7cdafbe89633f4f1
# export OPERAND_IMAGE_VERSION=1.26.5
export OPERAND_IMAGE_VERSION=1.26.3
# export OPERATOR_IMAGE_VERSION=4.13.3
export OPERATOR_IMAGE_VERSION=4.13.0-0.okd-scos-2023-05-25-085822
export POD_NAME=kube-apiserver-operator

REPO_DIR=$GOPATH/src/github.com/openshift/cluster-kube-apiserver-operator
KUBECONFIG=$HOME/.kube/config

$REPO_DIR/cluster-kube-apiserver-operator operator --config=./operator-config.yaml --kubeconfig=$KUBECONFIG --namespace openshift-kube-apiserver-operator
