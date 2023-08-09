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

OPERATOR_ENVS=$(oc get deploy kube-apiserver-operator -n openshift-kube-apiserver-operator -o json | jq '.spec.template.spec.containers[0].env')

export IMAGE=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="IMAGE") | .value' -r)
export OPERATOR_IMAGE=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="OPERATOR_IMAGE") | .value' -r)
export OPERAND_IMAGE_VERSION=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="OPERAND_IMAGE_VERSION") | .value' -r)
export OPERATOR_IMAGE_VERSION=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="OPERATOR_IMAGE_VERSION") | .value' -r)
export POD_NAME=kube-apiserver-operator

REPO_DIR=..
KUBECONFIG=$HOME/.kube/config

$REPO_DIR/cluster-kube-apiserver-operator operator --config=./operator-config.yaml --kubeconfig=$KUBECONFIG --namespace openshift-kube-apiserver-operator
