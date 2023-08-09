#!/usr/bin/bash

PROJECT=$(oc get infrastructure cluster -o json | jq -r '.status.platformStatus.gcp.projectID')
REGION=$(oc get infrastructure cluster -o json | jq -r '.status.platformStatus.gcp.region')
INFRA_ID=$(oc get infrastructure cluster -o json | jq -r '.status.infrastructureName')

KEYRING_NAME="${INFRA_ID}-kms"
KEY_NAME="kube-encryption"

gcloud kms keyrings create "${KEYRING_NAME}" --project "${PROJECT}" --location "${REGION}"
gcloud kms keys create "${KEY_NAME}" --project "${PROJECT}" --location "${REGION}" --keyring "${INFRA_ID}-kms" --purpose encryption

MASTER_NODE_NAME=$(oc get nodes -l node-role.kubernetes.io/control-plane -o json | jq -r '.items[0].metadata.name')
MASTER_NODE_ZONE=$(oc get node "${MASTER_NODE_NAME}" -o json | jq -r '.metadata.labels["topology.kubernetes.io/zone"]')

SERVICE_ACCOUNT=$(gcloud compute instances describe "${MASTER_NODE_NAME}" --zone "${MASTER_NODE_ZONE}" --project "${PROJECT}" | yq '.serviceAccounts[0].email')

gcloud kms keys add-iam-policy-binding "${KEY_NAME}" \
    --project "${PROJECT}" \
    --location "${REGION}" \
    --keyring "${KEYRING_NAME}" \
    --member "serviceAccount:${SERVICE_ACCOUNT}" \
    --role "roles/cloudkms.cryptoKeyEncrypterDecrypter"

echo "projects/${PROJECT}/locations/${REGION}/keyRings/${KEYRING_NAME}/cryptoKeys/${KEY_NAME}"
