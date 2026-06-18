package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openshift/api/features"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/encryption/encryptiondata"
	kmspluginlifecycle "github.com/openshift/library-go/pkg/operator/encryption/kms/pluginlifecycle"
	"github.com/openshift/library-go/pkg/operator/revisioncontroller"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

func KMSRevisionPostcondition(kubeClient kubernetes.Interface, featureGateAccessor featuregates.FeatureGateAccess) revisioncontroller.PostconditionFunc {
	return func(ctx context.Context, revision int32) (bool, error) {
		err := kmsRevisionPostcondition(ctx, kubeClient, featureGateAccessor, revision)
		return err == nil, err
	}
}

func kmsRevisionPostcondition(ctx context.Context, kubeClient kubernetes.Interface, featureGateAccessor featuregates.FeatureGateAccess, revision int32) error {
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

	encryptionSecretName := "encryption-config" + revSuffix
	encryptionSecret, err := kubeClient.CoreV1().Secrets(operatorclient.TargetNamespace).Get(ctx, encryptionSecretName, metav1.GetOptions{})
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

	expectedSidecars, err := buildExpectedKMSSidecars(encryptionSecret)
	if err != nil {
		return fmt.Errorf("kms revision post-check: failed to build expected sidecars for revision %d: %w", revision, err)
	}

	actualSidecars, err := extractKMSSidecarsFromPod(podCM)
	if err != nil {
		return fmt.Errorf("kms revision post-check: failed to extract sidecars from pod for revision %d: %w", revision, err)
	}

	sortContainersByName(expectedSidecars)
	sortContainersByName(actualSidecars)

	if !equality.Semantic.DeepEqual(expectedSidecars, actualSidecars) {
		return fmt.Errorf("kms revision post-check: revision %d has inconsistent KMS sidecars: expected=%v actual=%v",
			revision, containerNames(expectedSidecars), containerNames(actualSidecars))
	}

	klog.V(4).Infof("kms revision post-check passed for revision %d: sidecars=%v", revision, containerNames(expectedSidecars))
	return nil
}

func buildExpectedKMSSidecars(encryptionSecret *corev1.Secret) ([]corev1.Container, error) {
	cfg, err := encryptiondata.FromSecret(encryptionSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to extract encryption config from %s/%s secret: %w", encryptionSecret.Namespace, encryptionSecret.Name, err)
	}
	if cfg == nil {
		return nil, nil
	}

	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{{Name: "kube-apiserver"}},
	}

	if err := kmspluginlifecycle.NewKMSPluginBuilder().
		FromEncryptionConfig(encryptionSecret.Name, cfg).
		AsStaticPod().
		Apply(podSpec, "kube-apiserver"); err != nil {
		return nil, err
	}

	return podSpec.InitContainers, nil
}

// extractKMSSidecarsFromPod parses the kube-apiserver-pod ConfigMap and returns
// only the init containers that are KMS plugin sidecars.
func extractKMSSidecarsFromPod(podCM *corev1.ConfigMap) ([]corev1.Container, error) {
	podJSON, ok := podCM.Data["pod.yaml"]
	if !ok {
		return nil, nil
	}

	var pod corev1.Pod
	if err := json.Unmarshal([]byte(podJSON), &pod); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pod spec: %w", err)
	}

	var sidecars []corev1.Container
	for _, c := range pod.Spec.InitContainers {
		if isKMSSidecar(c.Name) {
			sidecars = append(sidecars, c)
		}
	}
	return sidecars, nil
}

// isKMSSidecar returns true if the container name matches the naming convention
// used by any known KMS sidecar provider ({provider-base}-{keyID}).
// TODO(bertinatto): move naming convention to library-go's pluginlifecycle
// package so this operator and the sidecar injection share a single source of truth.
func isKMSSidecar(containerName string) bool {
	for _, prefix := range kmsSidecarPrefixes {
		if strings.HasPrefix(containerName, prefix) {
			return true
		}
	}
	return false
}

var kmsSidecarPrefixes = []string{
	"vault-kms-plugin-",
}

func sortContainersByName(containers []corev1.Container) {
	sort.Slice(containers, func(i, j int) bool {
		return containers[i].Name < containers[j].Name
	})
}

func containerNames(containers []corev1.Container) []string {
	names := make([]string, len(containers))
	for i, c := range containers {
		names[i] = c.Name
	}
	return names
}
