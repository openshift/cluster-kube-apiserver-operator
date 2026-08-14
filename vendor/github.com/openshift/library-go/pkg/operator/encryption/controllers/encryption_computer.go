package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

var _ EncryptionConfigurationComputer = &EncryptionComputer{}

// EncryptionComputer is backed by the running key and state controllers and
// allows computing their outputs without side effects.
type EncryptionComputer struct {
	keyController   *keyController
	stateController *stateController
	// syncCtx holds a single rate-limiting queue whose goroutine lives for the
	// lifetime of this object. Re-queue requests issued during computation are
	// intentionally discarded.
	syncCtx factory.SyncContext
}

// NewEncryptionComputer creates an EncryptionComputer backed by the same
// controller instances returned by NewKeyController and NewStateController.
func NewEncryptionComputer(keyCtrl *keyController, stateCtrl *stateController) *EncryptionComputer {
	return &EncryptionComputer{
		keyController:   keyCtrl,
		stateController: stateCtrl,
		syncCtx: factory.NewSyncContext(
			"EncryptionConfigurationComputer",
			events.NewLoggingEventRecorder("encryption-configuration-computer", clock.RealClock{}),
		),
	}
}

// ComputeEncryptionConfiguration implements EncryptionConfigurationComputer.
// It computes the encryption configuration that would result after creating
// the next key, giving the KMS preflight deployer the configuration it needs
// to test the plugin before the key is actually created.
//
// It unconditionally skips the KMS preflight gate: the preflight controller
// calls this method to obtain the configuration it will deploy for testing,
// so blocking on a result that does not exist yet would deadlock.
func (e *EncryptionComputer) ComputeEncryptionConfiguration(ctx context.Context) (*corev1.Secret, error) {
	newKeySecret, err := e.keyController.computeKeySecretSkippingPreflight(ctx, e.syncCtx)
	if err != nil {
		return nil, err
	}

	sc := e.stateController
	listKeySecretsFn := sc.listKeySecretsFn
	if newKeySecret != nil {
		listKeySecretsFn = func(ctx context.Context) ([]*corev1.Secret, error) {
			existing, err := sc.listKeySecretsFn(ctx)
			if err != nil {
				return nil, err
			}
			return append(existing, newKeySecret), nil
		}
	}

	secret, _, err := sc.computeEncryptionConfigSecretWithCustomListKeySecretFn(ctx, e.syncCtx.Queue(), listKeySecretsFn)
	return secret, err
}
