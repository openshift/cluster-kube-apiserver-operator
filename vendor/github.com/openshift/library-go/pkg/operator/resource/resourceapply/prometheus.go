package resourceapply

import (
	"fmt"

	"github.com/ghodss/yaml"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/openshift/library-go/pkg/operator/events"
)

var serviceMonitorGVR = schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "servicemonitors"}

// ApplyServiceMonitor applies the Prometheus service monitor.
func ApplyServiceMonitor(client dynamic.Interface, recorder events.Recorder, serviceMonitorBytes []byte) (bool, error) {
	monitorJSON, err := yaml.YAMLToJSON(serviceMonitorBytes)
	if err != nil {
		return false, err
	}

	monitorObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, monitorJSON)
	if err != nil {
		return false, err
	}

	required, ok := monitorObj.(*unstructured.Unstructured)
	if !ok {
		return false, fmt.Errorf("unexpected object in %t", monitorObj)
	}

	namespace := required.GetNamespace()

	existing, err := client.Resource(serviceMonitorGVR).Namespace(namespace).Get(required.GetName(), metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, createErr := client.Resource(serviceMonitorGVR).Namespace(namespace).Create(required)
		if createErr != nil {
			recorder.Warningf("ServiceMonitorCreateFailed", "Failed to create ServiceMonitor.monitoring.coreos.com/v1: %v", createErr)
			return true, createErr
		}
		recorder.Eventf("ServiceMonitorCreated", "Created ServiceMonitor.monitoring.coreos.com/v1 because it was missing")
		return true, nil
	}

	existingSpec, _, err := unstructured.NestedFieldNoCopy(existing.UnstructuredContent(), "spec")
	if err != nil {
		return false, err
	}

	requiredSpec, _, err := unstructured.NestedFieldNoCopy(required.UnstructuredContent(), "spec")
	if err != nil {
		return false, err
	}

	if equality.Semantic.DeepEqual(existingSpec, requiredSpec) {
		return false, nil
	}

	if err := unstructured.SetNestedField(existing.UnstructuredContent(), requiredSpec, "spec"); err != nil {
		return true, err
	}

	if _, err = client.Resource(serviceMonitorGVR).Namespace(namespace).Update(existing); err != nil {
		recorder.Warningf("ServiceMonitorUpdateFailed", "Failed to update ServiceMonitor.monitoring.coreos.com/v1: %v", err)
		return true, err
	}

	recorder.Eventf("ServiceMonitorUpdated", "Updated ServiceMonitor.monitoring.coreos.com/v1 because it changed")
	return true, err
}
