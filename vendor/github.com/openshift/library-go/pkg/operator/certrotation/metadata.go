package certrotation

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ensureOwnerRefAndTLSAnnotations(meta *metav1.ObjectMeta, owner *metav1.OwnerReference, additionalAnnotations AdditionalAnnotations) []string {
	updateReasons := []string{}
	// no ownerReference set
	if owner != nil && ensureOwnerReference(meta, owner) {
		updateReasons = append(updateReasons, fmt.Sprintf("owner reference updated to %#v", owner))
	}
	// ownership annotations not set
	if additionalAnnotations.EnsureTLSMetadataUpdate(meta) {
		updateReasons = append(updateReasons, fmt.Sprintf("annotations set to %#v", additionalAnnotations))
	}
	return updateReasons
}
