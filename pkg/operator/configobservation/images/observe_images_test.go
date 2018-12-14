package images

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
)

type imageConfigTest struct {
	imageConfig                       *configv1.Image
	expectedInternalRegistryHostname  string
	expectedExternalRegistryHostnames []string
	expectedAllowedRegistries         []configv1.RegistryLocation
}

func TestObserveImageConfig(t *testing.T) {

	allowedRegistries := []configv1.RegistryLocation{
		{
			DomainName: "insecuredomain",
			Insecure:   true,
		},
		{
			DomainName: "securedomain1",
			Insecure:   true,
		},
		{
			DomainName: "securedomain2",
		},
	}

	tests := []imageConfigTest{
		{
			imageConfig: &configv1.Image{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.ImageStatus{
					InternalRegistryHostname: "docker-registry.openshift-image-registry.svc.cluster.local:5000",
				},
			},
			expectedInternalRegistryHostname: "docker-registry.openshift-image-registry.svc.cluster.local:5000",
		},
		{
			imageConfig: &configv1.Image{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.ImageSpec{
					ExternalRegistryHostnames: []string{},
				},
			},
			expectedExternalRegistryHostnames: nil,
		},
		{
			imageConfig: &configv1.Image{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.ImageSpec{
					ExternalRegistryHostnames: []string{"spec.external.host.com"},
				},
			},
			expectedExternalRegistryHostnames: []string{"spec.external.host.com"},
		},
		{
			imageConfig: &configv1.Image{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.ImageStatus{
					ExternalRegistryHostnames: []string{"status.external.host.com"},
				},
			},
			expectedExternalRegistryHostnames: []string{"status.external.host.com"},
		},
		{
			imageConfig: &configv1.Image{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.ImageSpec{
					ExternalRegistryHostnames: []string{"spec.external.host.com"},
				},
				Status: configv1.ImageStatus{
					ExternalRegistryHostnames: []string{"status.external.host.com"},
				},
			},
			expectedExternalRegistryHostnames: []string{"spec.external.host.com", "status.external.host.com"},
		},
		{
			imageConfig: &configv1.Image{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.ImageSpec{
					AllowedRegistriesForImport: allowedRegistries,
				},
			},
			expectedAllowedRegistries: allowedRegistries,
		},
		{
			imageConfig: &configv1.Image{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.ImageSpec{
					AllowedRegistriesForImport: []configv1.RegistryLocation{},
				},
			},
			expectedAllowedRegistries: nil,
		},
	}

	for _, tc := range tests {
		indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
		indexer.Add(tc.imageConfig)
		listers := configobservation.Listers{
			ImageConfigLister: configlistersv1.NewImageLister(indexer),
			ImageConfigSynced: func() bool { return true },
		}
		unsyncedlisters := configobservation.Listers{
			ImageConfigLister: configlistersv1.NewImageLister(indexer),
			ImageConfigSynced: func() bool { return false },
		}

		result, errs := ObserveInternalRegistryHostname(listers, events.NewInMemoryRecorder(""), map[string]interface{}{})
		if len(errs) != 0 {
			t.Fatalf("unexpected error: %v", errs)
		}
		internalRegistryHostname, _, err := unstructured.NestedString(result, "imagePolicyConfig", "internalRegistryHostname")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if internalRegistryHostname != tc.expectedInternalRegistryHostname {
			t.Errorf("expected internal registry hostname: %s, got %s", tc.expectedInternalRegistryHostname, internalRegistryHostname)
		}

		// When the cache is not synced, the result should be the previously observed
		// configuration.
		newResult, errs := ObserveInternalRegistryHostname(unsyncedlisters, events.NewInMemoryRecorder("test"), result)
		if len(errs) != 0 {
			t.Fatalf("unexpected error: %v", errs)
		}
		if !reflect.DeepEqual(result, newResult) {
			t.Errorf("got: \n%#v\nexpected: \n%#v", newResult, result)
		}

		result, errs = ObserveExternalRegistryHostnames(listers, events.NewInMemoryRecorder(""), map[string]interface{}{})
		if len(errs) != 0 {
			t.Fatalf("unexpected error: %v", errs)
		}
		o, _, err := unstructured.NestedSlice(result, "imagePolicyConfig", "externalRegistryHostnames")
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(o); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		externalRegistryHostnames := []string{}
		if err := json.NewDecoder(buf).Decode(&externalRegistryHostnames); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(externalRegistryHostnames, tc.expectedExternalRegistryHostnames) {
			t.Errorf("got: \n%#v\nexpected: \n%#v", externalRegistryHostnames, tc.expectedExternalRegistryHostnames)
		}

		// When the cache is not synced, the result should be the previously observed
		// configuration.
		newResult, errs = ObserveExternalRegistryHostnames(unsyncedlisters, events.NewInMemoryRecorder(""), result)
		if len(errs) != 0 {
			t.Fatalf("unexpected error: %v", errs)
		}
		if !reflect.DeepEqual(result, newResult) {
			t.Errorf("got: \n%#v\nexpected: \n%#v", newResult, result)
		}

		result, errs = ObserveAllowedRegistriesForImport(listers, events.NewInMemoryRecorder(""), map[string]interface{}{})
		if len(errs) != 0 {
			t.Fatalf("unexpected error: %v", errs)
		}
		o, _, err = unstructured.NestedSlice(result, "imagePolicyConfig", "allowedRegistriesForImport")
		buf = &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(o); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		allowedRegistries := []configv1.RegistryLocation{}
		if err := json.NewDecoder(buf).Decode(&allowedRegistries); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(allowedRegistries, tc.expectedAllowedRegistries) {
			t.Errorf("got: \n%#v\nexpected: \n%#v", allowedRegistries, tc.expectedAllowedRegistries)
		}

		// When the cache is not synced, the result should be the previously observed
		// configuration.
		newResult, errs = ObserveAllowedRegistriesForImport(unsyncedlisters, events.NewInMemoryRecorder(""), result)
		if len(errs) != 0 {
			t.Fatalf("unexpected error: %v", errs)
		}
		if !reflect.DeepEqual(result, newResult) {
			t.Errorf("got: \n%#v\nexpected: \n%#v", newResult, result)
		}
	}
}
