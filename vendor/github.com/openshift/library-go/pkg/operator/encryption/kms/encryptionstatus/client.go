package encryptionstatus

import (
	"context"

	operatorv1 "github.com/openshift/api/operator/v1"
	applyoperatorv1 "github.com/openshift/client-go/operator/applyconfigurations/operator/v1"
)

// KMSEncryptionStatusClient reads and writes the KMSEncryptionStatus sub-field
// of an operator CR. Different controllers can own independent sub-fields by
// passing distinct fieldManager values per call, matching the
// OperatorClient.ApplyOperatorStatus pattern.
type KMSEncryptionStatusClient interface {
	GetKMSEncryptionStatus(ctx context.Context) (*operatorv1.KMSEncryptionStatus, error)
	ApplyKMSEncryptionStatus(ctx context.Context, fieldManager string, status *applyoperatorv1.KMSEncryptionStatusApplyConfiguration) error
}
