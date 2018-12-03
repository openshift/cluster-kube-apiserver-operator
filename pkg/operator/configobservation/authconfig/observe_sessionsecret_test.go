package authconfig

import (
	"testing"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	v12 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestObserveSessionSecret(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sessionSecretName,
			Namespace: sessionSecretNamespace,
		},
	}
	indexer.Add(secret)

	listers := configobservation.Listers{
		SecretLister: v12.NewSecretLister(indexer),
	}
	listers.SecretHasSynced = func() bool { return true }
	result, errs := ObserveSessionSecret(listers, nil, map[string]interface{}{})
	if len(errs) > 0 {
		t.Error("expected len(errs) == 0")
	}
	secretPath, _, err := unstructured.NestedString(result, "oauthConfig", "sessionConfig", "sessionSecretsFile")
	if err != nil {
		t.Fatal(err)
	}
	if secretPath != sessionSecretPath {
		t.Errorf("expected oauthConfig.sessionConfig.sessionSecretsFile: %s, got %s", sessionSecretPath, secretPath)
	}
}

func TestDeleteSessionSecret(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	listers := configobservation.Listers{
		SecretLister: v12.NewSecretLister(indexer),
	}
	listers.SecretHasSynced = func() bool { return true }
	existingConfig := map[string]interface{}{}
	oauthConfigSessionSecretsFilePath := []string{"oauthConfig", "sessionConfig", "sessionSecretsFile"}

	err := unstructured.SetNestedField(existingConfig, "foobar", oauthConfigSessionSecretsFilePath...)
	if err != nil {
		t.Fatal(err)
	}
	result, errs := ObserveSessionSecret(listers, nil, existingConfig)
	if len(errs) > 0 {
		t.Error("expected len(errs) == 0")
	}
	secretPath, _, err := unstructured.NestedString(result, "oauthConfig", "sessionConfig", "sessionSecretsFile")
	if err != nil {
		t.Fatal(err)
	}
	if len(secretPath) > 0 {
		t.Errorf("expected oauthConfig.sessionConfig.sessionSecretsFile: \"\", got %s", secretPath)
	}
}
