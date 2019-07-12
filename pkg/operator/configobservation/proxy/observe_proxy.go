package proxy

import (
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/configobservation"
)

func ObserveProxyEnvVars(genericListers configobserver.Listers, recorder events.Recorder, currentConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error) {
	listers := genericListers.(configobservation.Listers)
	prevObservedConfig := map[string]interface{}{}
	httpProxyPath := []string{"envVars", "HTTP_PROXY"}
	httpsProxyPath := []string{"envVars", "HTTPS_PROXY"}
	noProxyPath := []string{"envVars", "NO_PROXY"}

	observedConfig = map[string]interface{}{}
	proxyConfig, _ := listers.ProxyLister.Get("cluster")
	if len(proxyConfig.Status.HTTPProxy) > 0 {
		if err := unstructured.SetNestedField(observedConfig, proxyConfig.Status.HTTPProxy, httpProxyPath...); err != nil {
			errs = append(errs, err)
			return
		}
	}
	if len(proxyConfig.Status.HTTPSProxy) > 0 {
		if err := unstructured.SetNestedField(observedConfig, proxyConfig.Status.HTTPSProxy, httpsProxyPath...); err != nil {
			errs = append(errs, err)
			return
		}
	}
	if len(proxyConfig.Status.NoProxy) > 0 {
		if err := unstructured.SetNestedField(observedConfig, proxyConfig.Status.NoProxy, noProxyPath...); err != nil {
			errs = append(errs, err)
			return
		}
	}

	if len(errs) > 0 {
		return
	}

	if !reflect.DeepEqual(prevObservedConfig, proxyConfig) {
		recorder.Eventf("ObserveStorageUpdated", "Updated proxy env vars to %s", strings.Join(proxyConfig, ","))
	}

	return
}
