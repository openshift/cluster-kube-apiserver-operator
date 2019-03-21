package render

import (
	"reflect"
	"testing"
)

var (
	expectedClusterCIDR = []string{"10.128.0.0/14"}
	expectedServiceCIDR = []string{"172.30.0.0/16"}
	clusterAPIConfig    = `
apiVersion: machine.openshift.io/v1beta1
kind: Cluster
metadata:
  creationTimestamp: null
  name: cluster
  namespace: openshift-machine-api
spec:
  clusterNetwork:
    pods:
      cidrBlocks:
        - 10.128.0.0/14
    serviceDomain: ""
    services:
      cidrBlocks:
        - 172.30.0.0/16
  providerSpec: {}
status: {}
`
	networkConfig = `
apiVersion: config.openshift.io/v1
kind: Network
metadata:
  creationTimestamp: null
  name: cluster
spec:
  clusterNetwork:
    - cidr: 10.128.0.0/14
      hostPrefix: 23
  networkType: OpenShiftSDN
  serviceNetwork:
    - 172.30.0.0/16
status: {}
`
)

func TestDiscoverCIDRsFromNetwork(t *testing.T) {
	renderConfig := TemplateData{
		LockHostPath:   "",
		EtcdServerURLs: []string{""},
		EtcdServingCA:  "",
	}
	if err := discoverCIDRsFromNetwork([]byte(networkConfig), &renderConfig); err != nil {
		t.Errorf("failed discoverCIDRs: %v", err)
	}
	if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
	}
	if !reflect.DeepEqual(renderConfig.ServiceCIDR, expectedServiceCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ServiceCIDR, expectedServiceCIDR)
	}
}

func TestDiscoverCIDRsFromClusterAPI(t *testing.T) {
	renderConfig := TemplateData{
		LockHostPath:   "",
		EtcdServerURLs: []string{""},
		EtcdServingCA:  "",
	}
	if err := discoverCIDRsFromClusterAPI([]byte(clusterAPIConfig), &renderConfig); err != nil {
		t.Errorf("failed discoverCIDRs: %v", err)
	}
	if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
	}
	if !reflect.DeepEqual(renderConfig.ServiceCIDR, expectedServiceCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ServiceCIDR, expectedServiceCIDR)
	}
}

func TestDiscoverCIDRs(t *testing.T) {
	testCase := []struct {
		config []byte
	}{
		{
			config: []byte(networkConfig),
		},
		{
			config: []byte(clusterAPIConfig),
		},
	}

	for _, tc := range testCase {
		renderConfig := TemplateData{
			LockHostPath:   "",
			EtcdServerURLs: []string{""},
			EtcdServingCA:  "",
		}

		if err := discoverCIDRs(tc.config, &renderConfig); err != nil {
			t.Errorf("failed to discoverCIDRs: %v", err)
		}

		if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
			t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
		}
		if !reflect.DeepEqual(renderConfig.ServiceCIDR, expectedServiceCIDR) {
			t.Errorf("Got: %v, expected: %v", renderConfig.ServiceCIDR, expectedServiceCIDR)
		}
	}
}
