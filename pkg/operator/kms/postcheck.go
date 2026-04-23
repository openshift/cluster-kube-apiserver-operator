package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/revisioncontroller"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

var (
	apiserverScheme = runtime.NewScheme()
	apiserverCodecs = serializer.NewCodecFactory(apiserverScheme)
)

func init() {
	utilruntime.Must(apiserverv1.AddToScheme(apiserverScheme))
}

// TODO: this needs to be moved to library-go

func KMSRevisionPostCheck(
	kubeClient kubernetes.Interface,
	featureGateAccessor featuregates.FeatureGateAccess,
) revisioncontroller.RevisionPostCheckFunc {
	return func(ctx context.Context, revision int32) error {
		return validateKMSRevision(ctx, kubeClient, featureGateAccessor, revision)
	}
}

func validateKMSRevision(ctx context.Context, kubeClient kubernetes.Interface, featureGateAccessor featuregates.FeatureGateAccess, revision int32) error {
	if !featureGateAccessor.AreInitialFeatureGatesObserved() {
		return nil
	}
	featureGates, err := featureGateAccessor.CurrentFeatureGates()
	if err != nil {
		return nil
	}
	if !featureGates.Enabled(features.FeatureGateKMSEncryption) {
		return nil
	}

	revSuffix := fmt.Sprintf("-%d", revision)

	encryptionSecret, err := kubeClient.CoreV1().Secrets(operatorclient.TargetNamespace).Get(ctx, "encryption-config"+revSuffix, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kms revision post-check: failed to get encryption-config for revision %d: %w", revision, err)
	}

	podCM, err := kubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, "kube-apiserver-pod"+revSuffix, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kms revision post-check: failed to get kube-apiserver-pod for revision %d: %w", revision, err)
	}

	kmsConfigured := hasKMSEncryptionProvider(encryptionSecret)
	// FIXME(bertinatto): plugin name is hardcoded here
	kmsInPod := podHasKMSSidecar(podCM, "kms-plugin")
	if kmsConfigured != kmsInPod {
		return fmt.Errorf("kms revision post-check: revision %d has mismatched KMS state: kmsConfigured=%v kmsInPod=%v", revision, kmsConfigured, kmsInPod)
	}

	klog.V(4).Infof("kms revision post-check passed for revision %d: kmsConfigured=%v kmsInPod=%v", revision, kmsConfigured, kmsInPod)
	return nil
}

func podHasKMSSidecar(podCM *corev1.ConfigMap, sidecarName string) bool {
	podYAML, ok := podCM.Data["pod.yaml"]
	if !ok {
		return false
	}

	var pod corev1.Pod
	if err := json.Unmarshal([]byte(podYAML), &pod); err != nil {
		return false
	}

	return slices.ContainsFunc(pod.Spec.Containers, func(c corev1.Container) bool {
		return c.Name == sidecarName
	})
}

func hasKMSEncryptionProvider(encryptionConfig *corev1.Secret) bool {
	encryptionConfigBytes, ok := encryptionConfig.Data["encryption-config"]
	if !ok {
		return false
	}

	gvk := apiserverv1.SchemeGroupVersion.WithKind("EncryptionConfiguration")
	obj, _, err := apiserverCodecs.UniversalDeserializer().Decode(encryptionConfigBytes, &gvk, nil)
	if err != nil {
		return false
	}

	config := obj.(*apiserverv1.EncryptionConfiguration)
	return slices.ContainsFunc(config.Resources, func(resource apiserverv1.ResourceConfiguration) bool {
		return slices.ContainsFunc(resource.Providers, func(provider apiserverv1.ProviderConfiguration) bool {
			return provider.KMS != nil
		})
	})
}
