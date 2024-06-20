package highcpuusagealertcontroller

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"os"
	"strconv"
	"testing"

	v1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
)

func TestSNOAlert(t *testing.T) {
	type args struct {
		clusterObjects      []runtime.Object
		enabledCapabilities []v1.ClusterVersionCapability
		cpuMode             v1.CPUPartitioningMode
	}
	tests := []struct {
		name       string
		args       args
		goldenFile string
		wantErr    bool
	}{
		{
			name:       "No node tuning capability",
			goldenFile: "./testdata/alert_8_cores.yaml",
			wantErr:    false,
		},
		{
			name: "Node tuning capability, but wrong cpu mode",
			args: args{
				enabledCapabilities: []v1.ClusterVersionCapability{v1.ClusterVersionCapabilityNodeTuning},
				cpuMode:             v1.CPUPartitioningNone,
			},
			goldenFile: "./testdata/alert_8_cores.yaml",
			wantErr:    false,
		},
		{
			name: "Node tuning capability,correct cpu mode, but no PerformanceProfile",
			args: args{
				enabledCapabilities: []v1.ClusterVersionCapability{v1.ClusterVersionCapabilityNodeTuning},
				cpuMode:             v1.CPUPartitioningAllNodes,
			},
			goldenFile: "./testdata/alert_8_cores.yaml",
			wantErr:    false,
		},
		{
			name: "Node tuning capability, correct cpu mode, correct PerformanceProfile",
			args: args{
				enabledCapabilities: []v1.ClusterVersionCapability{v1.ClusterVersionCapabilityNodeTuning},
				cpuMode:             v1.CPUPartitioningAllNodes,
				clusterObjects:      []runtime.Object{performanceProfileWithNodeSelector("node-role.kubernetes.io/master")},
			},
			goldenFile: "./testdata/alert_2_cores.yaml",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := snoAlert(context.Background(), clientWithObjects(tt.args.clusterObjects...), tt.args.enabledCapabilities, tt.args.cpuMode)
			if (err != nil) != tt.wantErr {
				t.Fatalf("snoAlert() error = %v, expectedErr = %v", err, tt.wantErr)
			}
			goldenFileAlert := readBytesFromFile(t, tt.goldenFile)

			if !bytes.Equal(got, goldenFileAlert) {
				t.Errorf("snoAlert() got = %v, goldenFile = %v", got, goldenFileAlert)
			}
		})
	}
}

func TestPerformanceProfileControlPlaneCores(t *testing.T) {
	tests := []struct {
		name           string
		clusterObjects []runtime.Object

		expectedCores       int
		expectedToFindCores bool
		expectedErr         bool
	}{
		{
			name:                "no performanceProfile",
			expectedToFindCores: false,
			expectedErr:         false,
		},
		{
			name:                "one performanceProfile",
			clusterObjects:      []runtime.Object{performanceProfileWithNodeSelector("node-role.kubernetes.io/master")},
			expectedCores:       2,
			expectedToFindCores: true,
			expectedErr:         false,
		},
		{
			name:                "only worker performanceProfile",
			clusterObjects:      []runtime.Object{performanceProfileWithNodeSelector("node-role.kubernetes.io/worker")},
			expectedToFindCores: false,
			expectedErr:         false,
		},
		{
			name: "multiple performanceProfiles",
			clusterObjects: []runtime.Object{
				performanceProfileWithNodeSelector("node-role.kubernetes.io/master"),
				performanceProfileWithNodeSelector("node-role.kubernetes.io/worker")},
			expectedCores:       2,
			expectedToFindCores: true,
			expectedErr:         false,
		},
		{
			name:                "invalid cpu set in performance profile",
			clusterObjects:      []runtime.Object{invalidCPUSetInvalidPerformanceProfile()},
			expectedToFindCores: false,
			expectedErr:         true,
		},
		{
			name:                "no node selector in performance profile",
			clusterObjects:      []runtime.Object{noNodeSelectorInvalidPerformanceProfile()},
			expectedToFindCores: false,
		},
		{
			name:                "no cpu set in performance profile",
			clusterObjects:      []runtime.Object{noCPUSetInvalidPerformanceProfile()},
			expectedToFindCores: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coresFound, isFound, err := performanceProfileControlPlaneCores(context.Background(), clientWithObjects(tt.clusterObjects...))
			if (err != nil) != tt.expectedErr {
				t.Fatalf("performanceProfileControlPlaneCores() error = %v, expectedErr = %v", err, tt.expectedErr)
			}
			if coresFound != tt.expectedCores {
				t.Errorf("performanceProfileControlPlaneCores() coresFound = %v, expectedCores = %v", coresFound, tt.expectedCores)
			}
			if isFound != tt.expectedToFindCores {
				t.Errorf("performanceProfileControlPlaneCores() isFound = %v, expectedToFindCores = %v", isFound, tt.expectedToFindCores)
			}
		})
	}
}

func clientWithObjects(objs ...runtime.Object) dynamic.Interface {
	scheme := runtime.NewScheme()
	return fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		performanceGroup: "PerformanceProfileList",
	}, objs...)
}

func performanceProfileWithNodeSelector(selector string) runtime.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "performance.openshift.io/v2",
			"kind":       "PerformanceProfile",
			"metadata": map[string]interface{}{
				"name": "performanceProfile" + strconv.Itoa(rand.Int()),
			},
			"spec": map[string]interface{}{
				"nodeSelector": map[string]interface{}{
					selector: "",
				},
				"cpu": map[string]interface{}{
					"isolated": "0-13",
					"reserved": "14-15",
				},
			},
		},
	}
}

func invalidCPUSetInvalidPerformanceProfile() runtime.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "performance.openshift.io/v2",
			"kind":       "PerformanceProfile",
			"metadata": map[string]interface{}{
				"name": "performanceProfile" + strconv.Itoa(rand.Int()),
			},
			"spec": map[string]interface{}{
				"nodeSelector": map[string]interface{}{
					"node-role.kubernetes.io/master": "",
				},
				"cpu": map[string]interface{}{
					"isolated": "0-13",
					"reserved": "14+15",
				},
			},
		},
	}
}

func noNodeSelectorInvalidPerformanceProfile() runtime.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "performance.openshift.io/v2",
			"kind":       "PerformanceProfile",
			"metadata": map[string]interface{}{
				"name": "performanceProfile" + strconv.Itoa(rand.Int()),
			},
			"spec": map[string]interface{}{
				"cpu": map[string]interface{}{
					"isolated": "0-13",
					"reserved": "14-15",
				},
			},
		},
	}
}

func noCPUSetInvalidPerformanceProfile() runtime.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "performance.openshift.io/v2",
			"kind":       "PerformanceProfile",
			"metadata": map[string]interface{}{
				"name": "performanceProfile" + strconv.Itoa(rand.Int()),
			},
			"spec": map[string]interface{}{
				"nodeSelector": map[string]interface{}{
					"node-role.kubernetes.io/master": "",
				},
			},
		},
	}
}

func readBytesFromFile(t *testing.T, filename string) []byte {
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	return data
}
