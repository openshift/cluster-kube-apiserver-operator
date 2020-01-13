package network

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/network"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

// ObserveRestrictedCIDRs watches the network configuration and updates the
// RestrictedEndpointsAdmission controller config.
func ObserveRestrictedCIDRs(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	var errs []error
	configPath := []string{"admission", "pluginConfig", "network.openshift.io/RestrictedEndpointsAdmission", "configuration"}

	admissionControllerConfig := unstructured.Unstructured{}
	prev, ok, err := unstructured.NestedMap(existingConfig, configPath...)
	if err != nil {
		errs = append(errs, err)
	}
	if ok && err == nil {
		// If we ever bump the API version, we'll have to do conversion here.
		admissionControllerConfig.SetUnstructuredContent(prev)
	}
	admissionControllerConfig.SetAPIVersion("network.openshift.io/v1")
	admissionControllerConfig.SetKind("RestrictedEndpointsAdmissionConfig")

	clusterCIDRs, err := network.GetClusterCIDRs(listers.NetworkLister, recorder)
	if err != nil {
		errs = append(errs, err)
	}

	serviceCIDR, err := network.GetServiceCIDR(listers.NetworkLister, recorder)
	if err != nil {
		errs = append(errs, err)
	}

	// If we weren't able to retrieve cidrs, then return the previous configuration
	if len(errs) > 0 || len(clusterCIDRs) == 0 || len(serviceCIDR) == 0 {
		previouslyObservedConfig := map[string]interface{}{}
		unstructured.SetNestedMap(previouslyObservedConfig, admissionControllerConfig.Object, configPath...)
		return previouslyObservedConfig, errs
	}

	// set observed values
	//  admission:
	//    pluginConfig:
	//      network.openshift.io/RestrictedEndpointsAdmission:
	//        configuration:
	//          version: network.openshift.io/v1
	//          kind: RestrictedEndpointsAdmissionConfig
	//          restrictedCIDRs:
	//            - 10.3.0.0/16 # ServiceCIDR
	//            - 10.2.0.0/16 # ClusterCIDR
	restrictedCIDRs := clusterCIDRs
	if len(serviceCIDR) > 0 {
		restrictedCIDRs = append(restrictedCIDRs, serviceCIDR)
	}

	if err := unstructured.SetNestedStringSlice(admissionControllerConfig.Object, restrictedCIDRs, "restrictedCIDRs"); err != nil {
		errs = append(errs, err)
	}

	observedConfig := map[string]interface{}{}
	unstructured.SetNestedMap(observedConfig, admissionControllerConfig.Object, configPath...)

	return observedConfig, errs
}

// ObserveServicesSubnet watches the network configuration and generates the
// servicesSubnet
func ObserveServicesSubnet(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	out := map[string]interface{}{}
	configPath := []string{"servicesSubnet"}

	prev, ok, err := unstructured.NestedString(existingConfig, configPath...)
	if err != nil {
		return out, []error{err}
	}
	if ok {
		if err := unstructured.SetNestedField(out, prev, configPath...); err != nil {
			return out, []error{err}
		}
	}

	errs := []error{}
	serviceCIDR, err := network.GetServiceCIDR(listers.NetworkLister, recorder)
	if err != nil {
		errs = append(errs, err)
	}

	if err := unstructured.SetNestedField(out, serviceCIDR, configPath...); err != nil {
		errs = append(errs, err)
	}

	return out, errs
}

// ObserveExternalIPPolicy observes the network configuration and generates the
// ExternalIPRanger admission controller accordingly.
func ObserveExternalIPPolicy(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	// set observed values
	//  admission:
	//    pluginConfig:
	//      network.openshift.io/ExternalIPRanger:
	//        configuration:
	//          version: network.openshift.io/v1
	//          kind: ExternalIPRangerAdmissionConfig
	//          externalIPNetworkCIDRs: [...]
	observedConfig := map[string]interface{}{}
	configPath := []string{"admission", "pluginConfig", "network.openshift.io/ExternalIPRanger", "configuration"}

	// For 4.1, we didn't expose the ExternalIP in the network spec so only need to block all IPs by default.

	// Policy, synthesize config
	// Simply by creating this configuration, the admission controller will
	// be enabled and block all IPs by default.
	admissionControllerConfig := unstructured.Unstructured{}
	admissionControllerConfig.SetAPIVersion("network.openshift.io/v1")
	admissionControllerConfig.SetKind("ExternalIPRangerAdmissionConfig")

	unstructured.SetNestedMap(observedConfig, admissionControllerConfig.Object, configPath...)

	return observedConfig, []error{}
}
