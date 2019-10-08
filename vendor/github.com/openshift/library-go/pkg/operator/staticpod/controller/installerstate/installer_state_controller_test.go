package installerstate

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/openshift/library-go/pkg/operator/events/eventstesting"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func newInstallerPod(name string, mutateStatusFn func(*corev1.PodStatus)) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test",
			Labels:    map[string]string{"app": "installer"},
		},
		Spec:   corev1.PodSpec{},
		Status: corev1.PodStatus{},
	}
	mutateStatusFn(&pod.Status)
	return pod
}

func TestInstallerStateController(t *testing.T) {
	tests := []struct {
		name           string
		pods           []runtime.Object
		evalConditions func(t *testing.T, conditions []operatorv1.OperatorCondition)
	}{
		{
			name: "should report pending pod",
			pods: []runtime.Object{
				newInstallerPod("installer-1", func(status *corev1.PodStatus) {
					status.Phase = corev1.PodPending
					status.Reason = "PendingReason"
					status.Message = "PendingMessage"
					status.StartTime = &metav1.Time{Time: time.Now().Add(-(maxToleratedPodPendingDuration + 5*time.Minute))}
				}),
			},
			evalConditions: func(t *testing.T, conditions []operatorv1.OperatorCondition) {
				podPendingCondition := v1helpers.FindOperatorCondition(conditions, "InstallerPodPendingDegraded")
				if podPendingCondition.Status != operatorv1.ConditionTrue {
					t.Errorf("expected InstallerPodPendingDegraded condition to be True")
				}
				podContainerWaitingCondition := v1helpers.FindOperatorCondition(conditions, "InstallerPodContainerWaitingDegraded")
				if podContainerWaitingCondition.Status != operatorv1.ConditionFalse {
					t.Errorf("expected InstallerPodPendingDegraded condition to be False")
				}
			},
		},
		{
			name: "should report pending pod with waiting container",
			pods: []runtime.Object{
				newInstallerPod("installer-1", func(status *corev1.PodStatus) {
					status.Phase = corev1.PodPending
					status.Reason = "PendingReason"
					status.Message = "PendingMessage"
					status.StartTime = &metav1.Time{Time: time.Now().Add(-(maxToleratedPodPendingDuration + 5*time.Minute))}
					status.ContainerStatuses = append(status.ContainerStatuses, corev1.ContainerStatus{Name: "test", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
						Reason:  "PodInitializing",
						Message: "initializing error",
					}}})
				}),
			},
			evalConditions: func(t *testing.T, conditions []operatorv1.OperatorCondition) {
				podPendingCondition := v1helpers.FindOperatorCondition(conditions, "InstallerPodPendingDegraded")
				if podPendingCondition.Status != operatorv1.ConditionTrue {
					t.Errorf("expected InstallerPodPendingDegraded condition to be True")
				}
				podContainerWaitingCondition := v1helpers.FindOperatorCondition(conditions, "InstallerPodContainerWaitingDegraded")
				if podContainerWaitingCondition.Status != operatorv1.ConditionTrue {
					t.Errorf("expected InstallerPodPendingDegraded condition to be True")
				}
			},
		},
		{
			name: "should report false when no pending pods",
			pods: []runtime.Object{
				newInstallerPod("installer-1", func(status *corev1.PodStatus) {
					status.Phase = corev1.PodRunning
					status.StartTime = &metav1.Time{Time: time.Now().Add(-(maxToleratedPodPendingDuration + 5*time.Minute))}
				}),
			},
			evalConditions: func(t *testing.T, conditions []operatorv1.OperatorCondition) {
				podPendingCondition := v1helpers.FindOperatorCondition(conditions, "InstallerPodPendingDegraded")
				if podPendingCondition.Status != operatorv1.ConditionFalse {
					t.Errorf("expected InstallerPodPendingDegraded condition to be False")
				}
				podContainerWaitingCondition := v1helpers.FindOperatorCondition(conditions, "InstallerPodContainerWaitingDegraded")
				if podContainerWaitingCondition.Status != operatorv1.ConditionFalse {
					t.Errorf("expected InstallerPodPendingDegraded condition to be False")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset(tt.pods...)
			kubeInformers := informers.NewSharedInformerFactoryWithOptions(kubeClient, 1*time.Minute, informers.WithNamespace("test"))
			stopCh := make(chan struct{})
			go kubeInformers.Start(stopCh)
			defer close(stopCh)

			fakeStaticPodOperatorClient := v1helpers.NewFakeStaticPodOperatorClient(&operatorv1.StaticPodOperatorSpec{}, &operatorv1.StaticPodOperatorStatus{}, nil, nil)
			eventRecorder := eventstesting.NewTestingEventRecorder(t)
			controller := NewInstallerStateController(kubeInformers, kubeClient.CoreV1(), fakeStaticPodOperatorClient, "test", eventRecorder)
			if err := controller.sync(); err != nil {
				t.Error(err)
				return
			}

			_, status, _, err := fakeStaticPodOperatorClient.GetOperatorState()
			if err != nil {
				t.Error(err)
				return
			}
			if tt.evalConditions != nil {
				tt.evalConditions(t, status.Conditions)
			}
		})
	}

}
