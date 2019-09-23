package errorreportcontroller

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/davecgh/go-spew/spew"
	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestCheckForPodCreationErrors(t *testing.T) {
	tests := []struct {
		name string

		events   []*corev1.Event
		expected operatorv1.OperatorCondition
	}{
		{
			name: "no events",
			expected: operatorv1.OperatorCondition{
				Type:   "StaticPodCreationDegraded",
				Reason: "NoWatchedErrorsAppeared",
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "events with network error",
			events: []*corev1.Event{
				{
					Count: 10,
					InvolvedObject: corev1.ObjectReference{
						Kind:      "Pod",
						Name:      "installer-5-control-plane-1",
						Namespace: "openshift-kube-apiserver",
					},
					Message: `(combined from similar events): Failed create pod sandbox: rpc error: code = Unknown desc = failed to create pod network sandbox k8s_installer-5-control-plane-1_openshift-kube-apiserver_900db7f3-d2ce-11e9-8fc8-005056be0641_0(121698f4862fd67157ca586cab18aefb048fe5d7b3bd87516098ac0e91a90a13): Multus: Err adding pod to network "openshift-sdn": Multus: error in invoke Delegate add - "openshift-sdn": failed to send CNI request: Post http://dummy/: dial unix /var/run/openshift-sdn/cniserver/socket: connect: connection refused`,
					ObjectMeta: metav1.ObjectMeta{
						Name:      "installer-5-control-plane-1.15c2b2de1c17a9cd",
						Namespace: "openshift-kube-apiserver",
					},
					Reason: "FailedCreatePodSandBox",
					Source: corev1.EventSource{
						Component: "kubelet",
						Host:      "control-plane-1",
					},
					Type: corev1.EventTypeWarning,
				},
			},
			expected: operatorv1.OperatorCondition{
				Type:   "StaticPodCreationDegraded",
				Reason: "NetworkError",
				Status: operatorv1.ConditionTrue,
			},
		},
	}

	testRegex := constructCumulativeRegex(messageToRootCauseMap)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := checkForPodCreationErrors(testRegex, test.events)

			if !reflect.DeepEqual(test.expected, actual) {
				t.Fatal(spew.Sdump(actual))
			}
		})
	}
}
