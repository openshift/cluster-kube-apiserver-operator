package network

import (
	"net"

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
// servicesSubnet (and bindAddress)
func ObserveServicesSubnet(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	out := map[string]interface{}{}
	servicesSubnetConfigPath := []string{"servicesSubnet"}
	bindAddressConfigPath := []string{"servingInfo", "bindAddress"}
	bindNetworkConfigPath := []string{"servingInfo", "bindNetwork"}

	previouslyObservedConfig, errs := extractPreviouslyObservedConfig(existingConfig, servicesSubnetConfigPath, bindAddressConfigPath, bindNetworkConfigPath)

	serviceCIDR, err := network.GetServiceCIDR(listers.NetworkLister, recorder)
	if err != nil {
		errs = append(errs, err)
		return previouslyObservedConfig, errs
	}

	if err := unstructured.SetNestedField(out, serviceCIDR, servicesSubnetConfigPath...); err != nil {
		errs = append(errs, err)
	}
	bindAddress := "0.0.0.0:6443"
	bindNetwork := "tcp4"
	// TODO: only do this in the single-stack IPv6 case once network.GetServiceCIDR()
	// supports dual-stack
	if serviceCIDR != "" {
		if ip, _, err := net.ParseCIDR(serviceCIDR); err != nil {
			errs = append(errs, err)
		} else if ip.To4() == nil {
			bindAddress = "[::]:6443"
			bindNetwork = "tcp6"
		}
	}
	if err := unstructured.SetNestedField(out, bindAddress, bindAddressConfigPath...); err != nil {
		errs = append(errs, err)
	}
	if err := unstructured.SetNestedField(out, bindNetwork, bindNetworkConfigPath...); err != nil {
		errs = append(errs, err)
	}

	return out, errs
}

// ObserveExternalIPPolicy observes the network configuration and generates the
// ExternalIPRanger admission controller accordingly.
func ObserveExternalIPPolicy(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	// set observed values
	//  admission:
	//    pluginConfig:
	//      network.openshift.io/ExternalIPRanger:
	//        configuration:
	//          version: network.openshift.io/v1
	//          kind: ExternalIPRangerAdmissionConfig
	//          externalIPNetworkCIDRs: [...]
	configPath := []string{"admission", "pluginConfig", "network.openshift.io/ExternalIPRanger", "configuration"}

	// Need to handle 3 cases:
	// 1. retrieval error: pass through existing, if it exists
	// 2. Null externalip policy: enable the externalip admission controller with deny-all
	// 3. Non-null policy: enable the externalip admission controller, pass through configuration.

	previouslyObservedConfig, errs := extractPreviouslyObservedConfig(existingConfig, configPath)

	externalIPPolicy, err := network.GetExternalIPPolicy(listers.NetworkLister, recorder)
	if err != nil {
		errs = append(errs, err)
	}

	// called "ingress ips" in the controller
	autoExternalIPs, err := network.GetExternalIPAutoAssignCIDRs(listers.NetworkLister, recorder)
	if err != nil {
		errs = append(errs, err)
	}

	// Case 1: retrieval error. Pass through our existing configuration path,
	// if it exists.
	if len(errs) > 0 {
		return previouslyObservedConfig, errs
	}

	// Case 2, 3: Policy, synthesize config
	// Simply by creating this configuration, the admission controller will
	// be enabled and block all IPs by default.

	admissionControllerConfig := unstructured.Unstructured{}
	admissionControllerConfig.SetAPIVersion("network.openshift.io/v1")
	admissionControllerConfig.SetKind("ExternalIPRangerAdmissionConfig")

	conf := []string{}

	if externalIPPolicy != nil {
		for _, cidr := range externalIPPolicy.RejectedCIDRs {
			conf = append(conf, "!"+cidr)
		}
		for _, cidr := range externalIPPolicy.AllowedCIDRs {
			conf = append(conf, cidr)
		}
	}

	// Logic carried forward from 3.X. The "autoExternalIPs" is internally
	// called IngressIPs (this is a nasty naming collision). We need to
	// activate a special mode in the controller if this is enabled.
	allowIngressIP := len(autoExternalIPs) > 0

	if len(conf) > 0 {
		unstructured.SetNestedStringSlice(admissionControllerConfig.Object, conf, "externalIPNetworkCIDRs")
	}
	unstructured.SetNestedField(admissionControllerConfig.Object, allowIngressIP, "allowIngressIP")
	observedConfig := map[string]interface{}{}
	unstructured.SetNestedMap(observedConfig, admissionControllerConfig.Object, configPath...)

	return observedConfig, errs
}

// extractPreviouslyObservedConfig extracts the previously observed config from the existing config.
func extractPreviouslyObservedConfig(existing map[string]interface{}, paths ...[]string) (map[string]interface{}, []error) {
	var errs []error
	previous := map[string]interface{}{}
	for _, fields := range paths {
		value, found, err := unstructured.NestedFieldCopy(existing, fields...)
		if !found {
			continue
		}
		if err != nil {
			errs = append(errs, err)
		}
		err = unstructured.SetNestedField(previous, value, fields...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return previous, errs
}
