#!/bin/bash

# Run this script from root of repository
# Usage: bash hack-kms/run-operator-locally.sh

# disable CVO on cluster
oc scale -n openshift-cluster-version deploy cluster-version-operator --replicas 0

# scale down operator running on cluster
oc scale -n openshift-kube-apiserver-operator deploy kube-apiserver-operator --replicas=0

OPERATOR_ENVS=$(oc get deploy kube-apiserver-operator -n openshift-kube-apiserver-operator -o json | jq '.spec.template.spec.containers[0].env')

export IMAGE=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="IMAGE") | .value' -r)
export OPERATOR_IMAGE=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="OPERATOR_IMAGE") | .value' -r)
export OPERAND_IMAGE_VERSION=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="OPERAND_IMAGE_VERSION") | .value' -r)
export OPERATOR_IMAGE_VERSION=$(echo "${OPERATOR_ENVS[@]}" | jq '.[] | select(.name=="OPERATOR_IMAGE_VERSION") | .value' -r)
export POD_NAME=kube-apiserver-operator

KUBECONFIG=$HOME/.kube/config

make build
./cluster-kube-apiserver-operator operator --config=./hack-kms/operator-config.yaml --kubeconfig=$KUBECONFIG --namespace openshift-kube-apiserver-operator
