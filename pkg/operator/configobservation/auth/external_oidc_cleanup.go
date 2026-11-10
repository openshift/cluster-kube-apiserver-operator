package auth

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"
)

// canRemoveDirectOIDCConfig checks that revision assembly has moved past direct
// OIDC. Running pods use revisioned copies, so their rollout need not finish.
func canRemoveDirectOIDCConfig(listers configobservation.Listers) (bool, error) {
	// Take one snapshot of the shared ConfigMap cache. Reading the rendered
	// config and revision status separately could mix different points in time.
	configMaps, err := listers.ConfigMapLister().ConfigMaps(operatorclient.TargetNamespace).List(labels.Everything())
	if err != nil {
		return false, fmt.Errorf("listing kube-apiserver ConfigMaps for direct OIDC cleanup: %w", err)
	}
	byName := make(map[string]*corev1.ConfigMap, len(configMaps))
	var latestRevision int64
	var latestStatus *corev1.ConfigMap
	for _, cm := range configMaps {
		byName[cm.Name] = cm
		if !strings.HasPrefix(cm.Name, "revision-status-") {
			continue
		}
		revision, err := strconv.ParseInt(strings.TrimPrefix(cm.Name, "revision-status-"), 10, 32)
		if err != nil || revision <= 0 || cm.Data["revision"] != strconv.FormatInt(revision, 10) {
			return false, fmt.Errorf("configmap %s/%s has invalid revision metadata", cm.Namespace, cm.Name)
		}
		if revision > latestRevision {
			latestRevision, latestStatus = revision, cm
		}
	}

	// A ready older webhook revision is insufficient: a newer unfinished
	// revision might already have copied a direct-OIDC config. The revision
	// controller runs one worker and marks ready only after copying resources.
	if latestStatus == nil || latestStatus.Annotations["operator.openshift.io/revision-ready"] != "true" {
		return false, nil
	}
	for _, name := range []string{"config", fmt.Sprintf("config-%d", latestRevision)} {
		cm := byName[name]
		if cm == nil {
			return false, nil
		}
		webhookOnly, err := configUsesWebhookOnly(cm)
		if err != nil || !webhookOnly {
			return false, err
		}
	}

	secretName := fmt.Sprintf("webhook-authenticator-%d", latestRevision)
	secret, err := listers.SecretLister().Secrets(operatorclient.TargetNamespace).Get(secretName)
	if errors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("getting secret %s/%s for direct OIDC cleanup: %w", operatorclient.TargetNamespace, secretName, err)
	}
	if errs := validateKubeconfigSecret(secret); len(errs) > 0 {
		return false, fmt.Errorf("secret %s/%s is invalid: %w", secret.Namespace, secret.Name, utilerrors.NewAggregate(errs))
	}
	return true, nil
}

// configUsesWebhookOnly checks the rendered authentication arguments, including
// unsupported overrides, rather than inferring them from the desired auth type.
func configUsesWebhookOnly(cm *corev1.ConfigMap) (bool, error) {
	raw, ok := cm.Data["config.yaml"]
	if !ok || len(raw) == 0 {
		return false, fmt.Errorf("configmap %s/%s is missing nonempty config.yaml", cm.Namespace, cm.Name)
	}
	config := map[string]any{}
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		return false, fmt.Errorf("parsing configmap %s/%s config.yaml: %w", cm.Namespace, cm.Name, err)
	}
	direct, _, err := unstructured.NestedStringSlice(config, authConfigPath...)
	if err != nil {
		return false, fmt.Errorf("reading direct OIDC arguments from configmap %s/%s: %w", cm.Namespace, cm.Name, err)
	}
	if len(direct) > 0 {
		return false, nil
	}
	for _, arg := range []struct {
		path     []string
		expected []any
	}{
		{webhookTokenAuthenticatorPath, webhookTokenAuthenticatorFile},
		{webhookTokenAuthenticatorVersionPath, webhookTokenAuthenticatorVersion},
	} {
		value, _, err := unstructured.NestedSlice(config, arg.path...)
		if err != nil {
			return false, fmt.Errorf("reading webhook arguments from configmap %s/%s: %w", cm.Namespace, cm.Name, err)
		}
		if !equality.Semantic.DeepEqual(arg.expected, value) {
			return false, nil
		}
	}
	return true, nil
}
