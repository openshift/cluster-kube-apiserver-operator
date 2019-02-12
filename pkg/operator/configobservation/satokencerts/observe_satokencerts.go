package satokencerts

import (
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

// ObserveSATokenCerts checks which configmaps exist and bases the configuration for verifying sa tokens on them.
// There are two possible sources: the installer and the kube-controller-manger-operator, but we wire to the target namespace
// to avoid setting a config we cannot fulfill which would crash the kube-apiserver.
func ObserveSATokenCerts(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	// copy non-empty .serviceAccountPublicKeyFiles from existingConfig to prevObservedConfig
	saTokenCertsPath := []string{"serviceAccountPublicKeyFiles"}
	existingSATokenCerts, _, err := unstructured.NestedStringSlice(existingConfig, saTokenCertsPath...)
	if err != nil {
		return prevObservedConfig, append(errs, err)
	}
	if len(existingSATokenCerts) > 0 {
		if err := unstructured.SetNestedStringSlice(prevObservedConfig, existingSATokenCerts, saTokenCertsPath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}
	saTokenCertDirs := []string{}

	// add initial-sa-token-signing-certs configmap mount path to saTokenCertDirs if configmap exists
	initialCerts, err := listers.ConfigmapLister.ConfigMaps(operatorclient.TargetNamespace).Get("initial-sa-token-signing-certs")
	switch {
	case errors.IsNotFound(err):
		// do nothing because we aren't going to add a path to a missing configmap
	case err != nil:
		// we had an error, return what we had before and exit. this really shouldn't happen
		return prevObservedConfig, append(errs, err)
	case len(initialCerts.Data) > 0:
		// this means we have this configmap and it has values, so wire up the directory
		saTokenCertDirs = append(saTokenCertDirs, "/etc/kubernetes/static-pod-resources/configmaps/initial-sa-token-signing-certs")
	default:
		// do nothing because aren't going to add a path to a configmap with no files
	}

	kcmCerts, err := listers.ConfigmapLister.ConfigMaps(operatorclient.TargetNamespace).Get("kube-controller-manager-sa-token-signing-certs")
	switch {
	case errors.IsNotFound(err):
		// do nothing because we aren't going to add a path to a missing configmap
	case err != nil:
		// we had an error, return what we had before and exit. this really shouldn't happen
		return prevObservedConfig, append(errs, err)
	case len(kcmCerts.Data) > 0:
		// this means we have this configmap and it has values, so wire up the directory
		saTokenCertDirs = append(saTokenCertDirs, "/etc/kubernetes/static-pod-resources/configmaps/kube-controller-manager-sa-token-signing-certs")
	default:
		// do nothing because aren't going to add a path to a configmap with no files
	}

	if len(saTokenCertDirs) > 0 {
		if err := unstructured.SetNestedStringSlice(observedConfig, saTokenCertDirs, saTokenCertsPath...); err != nil {
			errs = append(errs, err)
		}
	}

	if !equality.Semantic.DeepEqual(existingSATokenCerts, saTokenCertDirs) {
		recorder.Eventf("ObserveSATokenCerts", "serviceAccountPublicKeyFiles changed to %v", saTokenCertDirs)
	}

	return observedConfig, errs
}
