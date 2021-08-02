package startupmonitorreadiness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewPodHasStateRunning(t *testing.T) {
	scenarios := []struct {
		name            string
		healthy         bool
		reason          string
		msg             string
		monitorRevision int
		nodeName        string

		initialObjects []runtime.Object
	}{
		{
			name:            "scenario 1: happy path",
			healthy:         true,
			monitorRevision: 3,
			nodeName:        "master-1",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas", "master-1")},
		},

		{
			name:     "scenario 2: no pod",
			healthy:  false,
			reason:   "PodNotRunning",
			msg:      "unable to check the pod's status, waiting for Kube API server pod to show up",
			nodeName: "master-1",
		},

		{
			name:           "scenario 3: pending pod",
			healthy:        false,
			reason:         "PodNodReady",
			msg:            "waiting for Kube API server pod to be in PodRunning phase, the current phase is Pending",
			nodeName:       "master-1",
			initialObjects: []runtime.Object{newPod(corev1.PodPending, corev1.ConditionTrue, "3", "kas", "master-1")},
		},

		{
			name:           "scenario 4: not ready pod",
			healthy:        false,
			reason:         "PodNodReady",
			msg:            "waiting for Kube API server pod to have PodReady state set to true",
			nodeName:       "master-1",
			initialObjects: []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionFalse, "3", "kas", "master-1")},
		},

		{
			name:            "scenario 5: unexpected revision",
			healthy:         false,
			reason:          "UnexpectedRevision",
			msg:             "the running Kube API (kas) is at unexpected revision 4, expected 3",
			nodeName:        "master-1",
			monitorRevision: 3,
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "4", "kas", "master-1")},
		},

		{
			name:            "scenario 6: unexpected node name",
			healthy:         false,
			reason:          "PodNotFound",
			msg:             "unable to check the pod's status, haven't found a pod that would match the current node name master-2, checked 1 Kube API server pods",
			nodeName:        "master-2",
			monitorRevision: 3,
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "4", "kas", "master-1")},
		},

		{
			name:            "scenario 7: multiple pods",
			healthy:         true,
			monitorRevision: 3,
			nodeName:        "master-1",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas", "master-1"), newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas-2", "master-2")},
		},

		{
			name:            "scenario 8: multiple pods on the same node",
			healthy:         false,
			monitorRevision: 3,
			nodeName:        "master-1",
			reason:          "PodListError",
			msg:             "unable to check the pod's status: found multiple pods ([kas kas-2]) matching the provided node name master-1",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas", "master-1"), newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas-2", "master-1")},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			fakeKubeClient := fake.NewSimpleClientset(scenario.initialObjects...)

			// act and validate
			doCheckAndValidate(t, func() (bool, string, string) {
				return newPodRunning(fakeKubeClient.CoreV1().Pods("openshift-kube-apiserver"), scenario.monitorRevision, scenario.nodeName)(context.TODO())
			}, scenario.healthy, scenario.reason, scenario.msg)
		})
	}
}

func TestNoOldRevisionPodExists(t *testing.T) {
	scenarios := []struct {
		name    string
		healthy bool
		reason  string
		msg     string

		monitorRevision int
		nodeName        string
		initialObjects  []runtime.Object
	}{
		{
			name:            "scenario 1: happy path",
			healthy:         false,
			monitorRevision: 3,
			reason:          "UnexpectedRevision",
			msg:             "the running Kube API (kas) is at unexpected revision 2, expected 3",
			nodeName:        "master-1",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "2", "kas", "master-1")},
		},

		{
			name:            "scenario 2: no pod",
			healthy:         true,
			monitorRevision: 3,
			reason:          "PodNotRunning",
			msg:             "waiting for Kube API server pod to show up",
			nodeName:        "master-1",
		},

		{
			name:            "scenario 3: unexpected node name",
			healthy:         false,
			monitorRevision: 3,
			reason:          "PodNotFound",
			msg:             "unable to check a revision, haven't found a pod that would match the current node name master-2, checked 1 Kube API server pods",
			nodeName:        "master-2",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "2", "kas", "master-1")},
		},

		{
			name:            "scenario 4: multiple pods",
			healthy:         true,
			monitorRevision: 3,
			nodeName:        "master-1",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas", "master-1"), newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas-2", "master-2")},
		},

		{
			name:            "scenario 5: multiple pods on the same node",
			healthy:         false,
			monitorRevision: 3,
			nodeName:        "master-1",
			reason:          "PodListError",
			msg:             "unable to check a revision: found multiple pods ([kas kas-2]) matching the provided node name master-1",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas", "master-1"), newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas-2", "master-1")},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			fakeKubeClient := fake.NewSimpleClientset(scenario.initialObjects...)

			// act and validate
			doCheckAndValidate(t, func() (bool, string, string) {
				return noOldRevisionPodExists(fakeKubeClient.CoreV1().Pods("openshift-kube-apiserver"), scenario.monitorRevision, scenario.nodeName)(context.TODO())
			}, scenario.healthy, scenario.reason, scenario.msg)
		})
	}

}

func TestNewRevisionPodExists(t *testing.T) {
	scenarios := []struct {
		name    string
		healthy bool
		reason  string
		msg     string

		monitorRevision int
		nodeName        string
		initialObjects  []runtime.Object
	}{
		{
			name:            "scenario 1: happy path",
			healthy:         true,
			monitorRevision: 3,
			nodeName:        "master-1",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas", "master-1")},
		},

		{
			name:            "scenario 2: no pod",
			healthy:         false,
			monitorRevision: 3,
			reason:          "PodNotRunning",
			msg:             "waiting for Kube API server pod to show up",
		},

		{
			name:            "scenario 3: multiple pods",
			healthy:         true,
			monitorRevision: 4,
			nodeName:        "master-2",
			initialObjects: []runtime.Object{
				newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas-old", "master-1"),
				newPod(corev1.PodRunning, corev1.ConditionTrue, "4", "kas-new", "master-2"),
			},
		},

		{
			name:            "scenario 4: old running",
			healthy:         false,
			monitorRevision: 4,
			nodeName:        "master-1",
			initialObjects: []runtime.Object{
				newPod(corev1.PodRunning, corev1.ConditionTrue, "3", "kas-old", "master-1"),
			},
			reason: "UnexpectedRevision",
			msg:    "the running Kube API (kas-old) is at unexpected revision 3, expected 4",
		},

		{
			name:            "scenario 5: unexpected node name",
			healthy:         false,
			monitorRevision: 3,
			reason:          "PodNotFound",
			msg:             "unable to check a revision, haven't found a pod that would match the current node name master-2, checked 1 Kube API server pods",
			nodeName:        "master-2",
			initialObjects:  []runtime.Object{newPod(corev1.PodRunning, corev1.ConditionTrue, "2", "kas", "master-1")},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// test data
			fakeKubeClient := fake.NewSimpleClientset(scenario.initialObjects...)

			// act and validate
			doCheckAndValidate(t, func() (bool, string, string) {
				return newRevisionPodExists(fakeKubeClient.CoreV1().Pods("openshift-kube-apiserver"), scenario.monitorRevision, scenario.nodeName)(context.TODO())
			}, scenario.healthy, scenario.reason, scenario.msg)
		})
	}
}

func TestGoodReadyzEndpoint(t *testing.T) {
	scenarios := []struct {
		name        string
		healthy     bool
		reason      string
		msg         string
		rspWriterFn func(w http.ResponseWriter)
	}{

		{
			name:    "scenario 1: happy path, HTTP 200, empty reason and msg",
			healthy: true,
		},

		{
			name: "scenario 2: HTTP 500, unhealthy reason and msg",
			rspWriterFn: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("kube is on fire"))
			},
			healthy: false,
			reason:  "NotReady",
			msg:     "kube is on fire",
		},

		{
			name: "scenario 3: HTTP 500 on the 2nd call, unhealthy reason and msg",
			rspWriterFn: func() func(w http.ResponseWriter) {
				var counter int
				return func(w http.ResponseWriter) {
					counter++
					if counter == 2 {
						w.WriteHeader(http.StatusInternalServerError)
						w.Write([]byte(fmt.Sprintf("failed on %d invocation", counter)))
						return
					}
					w.WriteHeader(http.StatusOK)
				}
			}(),
			healthy: false,
			reason:  "NotReady",
			msg:     "failed on 2 invocation",
		},

		{
			name: "scenario 4: HTTP 500 on the 3nd call, unhealthy reason and msg",
			rspWriterFn: func() func(w http.ResponseWriter) {
				var counter int
				return func(w http.ResponseWriter) {
					counter++
					if counter == 3 {
						w.WriteHeader(http.StatusInternalServerError)
						w.Write([]byte(fmt.Sprintf("failed on %d invocation", counter)))
						return
					}
					w.WriteHeader(http.StatusOK)
				}
			}(),
			healthy: false,
			reason:  "NotReady",
			msg:     "failed on 3 invocation",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			//t.Parallel()
			// set up the server and the client
			rspWriterFn := func(w http.ResponseWriter) {
				fmt.Fprintf(w, "ok")
			}
			ts, client := setupServerClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/readyz" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(fmt.Sprintf("a req received at unexpected path: %v", r.URL.Path)))
					return
				}
				if r.URL.RawQuery != "verbose=true" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(fmt.Sprintf("unexpected query params received: %v", r.URL.RawQuery)))
					return
				}
				rspWriterFn(w)
			})
			defer ts.Close()

			// rewrite rsp handler if provided
			if scenario.rspWriterFn != nil {
				rspWriterFn = scenario.rspWriterFn
			}

			// act and validate
			doCheckAndValidate(t, func() (bool, string, string) {
				return goodReadyzEndpoint(client, ts.URL, 3, 50*time.Millisecond)(context.TODO())
			}, scenario.healthy, scenario.reason, scenario.msg)
		})
	}
}

func TestGoodHealthzEndpoint(t *testing.T) {
	scenarios := []struct {
		name        string
		healthy     bool
		reason      string
		msg         string
		rspWriterFn func(w http.ResponseWriter)
	}{
		{
			name:    "scenario 1: happy path, HTTP 200, empty reason and msg",
			healthy: true,
		},

		{
			name: "scenario 2: HTTP 500, unhealthy reason and msg",
			rspWriterFn: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("kube is on fire"))
			},
			healthy: false,
			reason:  "Unhealthy",
			msg:     "kube is on fire",
		},

		{
			name: "scenario 3: unexpected err from the server",
			rspWriterFn: func(w http.ResponseWriter) {
				panic("bum")
			},
			healthy: false,
			reason:  "UnhealthyError",
			// we don't check the entire rsp from the server
			msg: "/healthz?verbose=true\": EOF",
		},
		{
			name: "scenario 4: no rsp from the server",
			rspWriterFn: func(w http.ResponseWriter) {
				time.Sleep(2 * time.Second)
			},
			healthy: false,
			reason:  "UnhealthyError",
			// we don't check the entire rsp from the server
			msg: "context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// set up the server and the client
			rspWriterFn := func(w http.ResponseWriter) {
				fmt.Fprintf(w, "ok")
			}
			ts, client := setupServerClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(fmt.Sprintf("a req received at unexpected path: %v", r.URL.Path)))
					return
				}
				if r.URL.RawQuery != "verbose=true" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(fmt.Sprintf("unexpected query params received: %v", r.URL.RawQuery)))
					return
				}
				rspWriterFn(w)
			})
			defer ts.Close()

			// rewrite rsp handler if provided
			if scenario.rspWriterFn != nil {
				rspWriterFn = scenario.rspWriterFn
			}

			// act and validate
			doCheckAndValidate(t, func() (bool, string, string) { return goodHealthzEndpoint(client, ts.URL)(context.TODO()) }, scenario.healthy, scenario.reason, scenario.msg)
		})
	}
}

func TestHealthzEtcdEndpoint(t *testing.T) {
	scenarios := []struct {
		name        string
		healthy     bool
		reason      string
		msg         string
		rspWriterFn func(w http.ResponseWriter)
	}{
		{
			name:    "scenario 1: happy path, HTTP 200, empty reason and msg",
			healthy: true,
		},

		{
			name: "scenario 2: HTTP 500, unhealthy reason and msg",
			rspWriterFn: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("etcd is on fire"))
			},
			healthy: false,
			reason:  "EtcdUnhealthy",
			msg:     "etcd is on fire",
		},
		{
			name: "scenario 3: unexpected err from the server",
			rspWriterFn: func(w http.ResponseWriter) {
				panic("bum")
			},
			healthy: false,
			reason:  "EtcdUnhealthyError",
			// we don't check the entire rsp from the server
			msg: "/healthz/etcd\": EOF",
		},
		{
			name: "scenario 4: no rsp from the server",
			rspWriterFn: func(w http.ResponseWriter) {
				time.Sleep(2 * time.Second)
			},
			healthy: false,
			reason:  "EtcdUnhealthyError",
			// we don't check the entire rsp from the server
			msg: "context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// set up the server and the client
			rspWriterFn := func(w http.ResponseWriter) {
				fmt.Fprintf(w, "ok")
			}
			ts, client := setupServerClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz/etcd" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte("a req received at unexpected path"))
					return
				}
				rspWriterFn(w)
			})
			defer ts.Close()

			// rewrite rsp handler if provided
			if scenario.rspWriterFn != nil {
				rspWriterFn = scenario.rspWriterFn
			}

			// act and validate
			doCheckAndValidate(t, func() (bool, string, string) { return goodHealthzEtcdEndpoint(client, ts.URL)(context.TODO()) }, scenario.healthy, scenario.reason, scenario.msg)
		})
	}

}

func doCheckAndValidate(t *testing.T, checkFn func() (bool, string, string), expectedHealthy bool, expectedReason, expectedMessage string) {
	actualHealthy, actualReason, actualMsg := checkFn()
	if expectedHealthy != actualHealthy {
		t.Errorf("unexpected health condition (healthy=%v), expected healthy=%v", actualHealthy, expectedHealthy)
	}
	if expectedReason != actualReason {
		t.Errorf("unexpected reason %v, expected %v", actualReason, expectedReason)
	}
	if !strings.Contains(actualMsg, expectedMessage) {
		t.Errorf("unexpected message %v, expected %v", actualMsg, expectedMessage)
	}
	if len(expectedMessage) == 0 && len(actualMsg) > 0 {
		t.Errorf("unexpected message %v received (didn't expect a msg)", actualMsg)
	}
}

func setupServerClient(handlerFn http.HandlerFunc) (*httptest.Server, *http.Client) {
	// set up the server
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerFn(w, r)
	}))
	ts.EnableHTTP2 = true
	ts.Start()

	// set the client timeout
	client := ts.Client()
	client.Timeout = time.Second
	return ts, client
}

func newPod(phase corev1.PodPhase, ready corev1.ConditionStatus, revision, name, nodeName string) *corev1.Pod {
	pod := corev1.Pod{
		TypeMeta: v1.TypeMeta{Kind: "Pod"},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: "openshift-kube-apiserver",
			Labels: map[string]string{
				"revision":  revision,
				"apiserver": "true",
			}},
		Spec: corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase: phase,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: ready,
			}},
		},
	}

	return &pod
}
