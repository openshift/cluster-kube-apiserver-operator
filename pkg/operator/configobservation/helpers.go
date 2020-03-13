package configobservation

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SelectedPaths returns the unstructured filtered by the given paths, i.e. everything
// outside of them will be dropped. The returned data structure does not share anything
// with the input. In case of error for a path, that path is dropped.
func SelectedPaths(obj map[string]interface{}, pths ...[]string) map[string]interface{} {
	if obj == nil {
		return nil
	}

	ret := map[string]interface{}{}

	for _, p := range pths {
		x, found, err := unstructured.NestedFieldCopy(obj, p...)
		if err != nil {
			continue
		}
		if !found {
			continue
		}
		if err := unstructured.SetNestedField(ret, x, p...); err != nil {
			continue
		}
	}

	return ret
}
