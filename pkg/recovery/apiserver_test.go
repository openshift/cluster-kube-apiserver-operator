package recovery

import (
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
)

func int64Ptr(v int64) *int64 {
	return &v
}

func TestApiserverRecoveryPod(t *testing.T) {
	const image = "hyperkube:latest"

	tt := []struct {
		name          string
		apiserver     *Apiserver
		expectedPod   *corev1.Pod
		expectedError error
	}{
		{
			name: "",
			apiserver: &Apiserver{
				recoveryResourcesDir: "/tmp/static-pod-resources/recovery-kube-apiserver-pod",
				kubeApiserverStaticPod: &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "kube-apiserver-cert-syncer-42",
								Image: "not the image you were looking for",
							},
							{
								Name:  "kube-apiserver-42",
								Image: image,
							},
							{
								Name:  "kube-apiserver-cert-syncer-41",
								Image: "not the image you were looking for",
							},
						},
					},
				},
			},
			expectedPod: &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-recovery",
					Namespace: "openshift-kube-apiserver",
					Labels: map[string]string{
						"revision": "recovery",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "kube-apiserver-recovery",
							Image:   image,
							Command: []string{"hyperkube", "kube-apiserver"},
							Args:    []string{"--openshift-config=/etc/kubernetes/static-pod-resources/config.yaml"},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 7443,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "resource-dir",
									MountPath: "/etc/kubernetes/static-pod-resources",
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("150m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
							ImagePullPolicy:          corev1.PullIfNotPresent,
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "resource-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/tmp/static-pod-resources/recovery-kube-apiserver-pod",
								},
							},
						},
					},
					PriorityClassName:             "system-node-critical",
					HostNetwork:                   true,
					TerminationGracePeriodSeconds: int64Ptr(0),
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
						},
					},
				},
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pod, err := tc.apiserver.recoveryPod()
			if !reflect.DeepEqual(tc.expectedError, err) {
				t.Fatalf("expected error %v, got %v", tc.expectedError, err)
			}

			if !reflect.DeepEqual(tc.expectedPod, pod) {
				t.Error(spew.Sprintf(
					"expected %#+v\n"+
						"got %#+v     \n"+
						"diff: %s",
					tc.expectedPod,
					pod,
					diff.ObjectReflectDiff(tc.expectedPod, pod),
				))
			}
		})
	}
}
