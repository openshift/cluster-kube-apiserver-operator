package encryption

import (
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	encryptionConfFilePath = "/etc/kubernetes/static-pod-resources/secrets/encryption-config/encryption-config"
	encryptionConfSecret   = "encryption-config"
)

type SecretLister interface {
	SecretLister() corev1listers.SecretLister
}

func NewEncryptionObserver(targetNamespace string, encryptionConfigPath []string) configobserver.ObserveConfigFunc {
	return func(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
		listers := genericListers.(SecretLister)
		var errs []error
		previouslyObservedConfig := map[string]interface{}{}

		existingEncryptionConfig, _, err := unstructured.NestedStringSlice(existingConfig, encryptionConfigPath...)
		if err != nil {
			return previouslyObservedConfig, append(errs, err)
		}

		if len(existingEncryptionConfig) > 0 {
			if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, existingEncryptionConfig, encryptionConfigPath...); err != nil {
				errs = append(errs, err)
			}
		}

		observedConfig := map[string]interface{}{}

		encryptionConfigSecret, err := listers.SecretLister().Secrets(targetNamespace).Get(encryptionConfSecret)
		if errors.IsNotFound(err) {
			recorder.Warningf("ObserveEncryptionConfigNotFound", "encryption config secret %s/%s not found", targetNamespace, encryptionConfSecret)
			// TODO what is the best thing to do here?
			// for now we do not unset the config as we are checking a synced version of the secret that could be deleted
			// return observedConfig, errs
			return previouslyObservedConfig, errs // do not append the not found error
		}
		if err != nil {
			recorder.Warningf("ObserveEncryptionConfigGetErr", "failed to get encryption config secret %s/%s: %v", targetNamespace, encryptionConfSecret, err)
			return previouslyObservedConfig, append(errs, err)
		}
		if len(encryptionConfigSecret.Data[encryptionConfSecret]) == 0 {
			recorder.Warningf("ObserveEncryptionConfigNoData", "encryption config secret %s/%s missing data", targetNamespace, encryptionConfSecret)
			return previouslyObservedConfig, errs
		}

		if err := unstructured.SetNestedStringSlice(observedConfig, []string{encryptionConfFilePath}, encryptionConfigPath...); err != nil {
			recorder.Warningf("ObserveEncryptionConfigFailedSet", "failed setting encryption config: %v", err)
			return previouslyObservedConfig, append(errs, err)
		}

		if !equality.Semantic.DeepEqual(existingEncryptionConfig, []string{encryptionConfFilePath}) {
			recorder.Eventf("ObserveEncryptionConfigChanged", "encryption config file changed from %s to %s", existingEncryptionConfig, encryptionConfFilePath)
		}

		return observedConfig, errs
	}
}
