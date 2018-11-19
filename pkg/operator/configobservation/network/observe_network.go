package network

import (
	"fmt"

	"github.com/ghodss/yaml"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
)

// ObserveRestrictedCIDRs observes list of restrictedCIDRs.
func ObserveRestrictedCIDRs(genericListers configobserver.Listers, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
	listers := genericListers.(configobservation.Listers)
	observedConfig = map[string]interface{}{}
	restrictedCIDRsPath := []string{"admissionPluginConfig", "openshift.io/RestrictedEndpointsAdmission", "configuration", "restrictedCIDRs"}

	clusterConfig, err := listers.ConfigmapLister.ConfigMaps("kube-system").Get("cluster-config-v1")
	if errors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: not found"))
		return
	}
	if err != nil {
		errs = append(errs, err)
		return
	}

	installConfigYaml, ok := clusterConfig.Data["install-config"]
	if !ok {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config not found"))
		return
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		errs = append(errs, err)
		return
	}

	// extract needed values
	//  data:
	//   install-config:
	//     networking:
	//       podCIDR: 10.2.0.0/16
	//       serviceCIDR: 10.3.0.0/16
	var restrictedCIDRs []string
	podCIDR, found, _ := unstructured.NestedString(installConfig, "networking", "podCIDR")
	if found {
		restrictedCIDRs = append(restrictedCIDRs, podCIDR)
	} else {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config/networking/podCIDR not found"))
	}
	serviceCIDR, found, _ := unstructured.NestedString(installConfig, "networking", "serviceCIDR")
	if found {
		restrictedCIDRs = append(restrictedCIDRs, serviceCIDR)
	} else {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config/networking/serviceCIDR not found"))
	}

	// set observed values
	//  admissionPluginConfig:
	//    openshift.io/RestrictedEndpointsAdmission:
	//	  configuration:
	//	    restrictedCIDRs:
	//	    - 10.3.0.0/16 # ServiceCIDR
	//	    - 10.2.0.0/16 # ClusterCIDR
	if len(restrictedCIDRs) > 0 {
		unstructured.SetNestedStringSlice(observedConfig, restrictedCIDRs, restrictedCIDRsPath...)
	}

	return
}
