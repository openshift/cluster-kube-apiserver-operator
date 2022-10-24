package serviceaccountissuercontroller

import (
	"context"
	"fmt"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1lister "github.com/openshift/client-go/config/listers/config/v1"
	fakeclient "github.com/openshift/client-go/operator/clientset/versioned/fake"
	operatorlistersv1 "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events/eventstesting"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"reflect"
	"testing"
	"time"
)

type fakeAuthLister struct {
	obj configv1.Authentication
}

func newFakeAuthLister(obj configv1.Authentication) configv1lister.AuthenticationLister {
	return &fakeAuthLister{obj: obj}
}

func (f *fakeAuthLister) List(selector labels.Selector) (ret []*configv1.Authentication, err error) {
	panic("implement me")
}

func (f *fakeAuthLister) Get(name string) (*configv1.Authentication, error) {
	return &f.obj, nil
}

type fakeOperatorLister struct {
	obj operatorv1.KubeAPIServer
}

func newFakeOperatorLister(obj operatorv1.KubeAPIServer) operatorlistersv1.KubeAPIServerLister {
	return &fakeOperatorLister{obj: obj}
}

func (f *fakeOperatorLister) List(selector labels.Selector) (ret []*operatorv1.KubeAPIServer, err error) {
	panic("implement me")
}

func (f *fakeOperatorLister) Get(name string) (*operatorv1.KubeAPIServer, error) {
	return &f.obj, nil
}

func generateIssuerStatus(count int) []operatorv1.ServiceAccountIssuerStatus {
	ret := []operatorv1.ServiceAccountIssuerStatus{}
	nowFn := func() time.Time { return time.Unix(1664788139, 0) }
	for i := 0; i <= count; i++ {
		ret = append(ret, operatorv1.ServiceAccountIssuerStatus{
			Name:           fmt.Sprintf("issuer-%d", i),
			ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
		})
	}
	return ret
}

func TestController(t *testing.T) {
	nowFn := func() time.Time { return time.Unix(1664788139, 0) }
	tests := []struct {
		name string

		authConfig configv1.Authentication
		operator   operatorv1.KubeAPIServer

		expectedStatus []operatorv1.ServiceAccountIssuerStatus
		expectedResync bool
		expectedErr    bool
	}{
		{
			name: "serviceaccountissuer is not being set and no trusted issuers should result in default to be set",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
			},
			expectedResync: true,
			expectedStatus: defaultServiceAccountIssuerValue,
		},
		{
			name: "serviceaccountissuer is set in auth config and should be copied to status while making default issuer trusted",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "newIssuer",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: defaultServiceAccountIssuerValue,
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "newIssuer",
				},
				{
					Name:           defaultServiceAccountIssuerValue[0].Name,
					ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
				},
			},
			expectedResync: true,
		},
		{
			name: "serviceaccountissuer is set in auth config to default and previous issuer should be trusted",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: defaultServiceAccountIssuerValue[0].Name,
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name: "previousIssuer",
						},
					},
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: defaultServiceAccountIssuerValue[0].Name,
				},
				{
					Name:           "previousIssuer",
					ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
				},
			},
			expectedResync: true,
		},
		{
			name: "serviceaccountissuer is set in auth config and should not be copied to status because of limit",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "newIssuer",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: append([]operatorv1.ServiceAccountIssuerStatus{{Name: "activeIssuer"}}, generateIssuerStatus(10)...),
				},
			},
			expectedStatus: append([]operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "activeIssuer",
				},
			}, generateIssuerStatus(10)...),
			expectedErr: true,
		},
		{
			name: "serviceaccountissuer is set in auth config and is already in status",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "currentIssuer",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name: "currentIssuer",
						},
					},
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "currentIssuer",
				},
			},
		},
		{
			name: "serviceaccountissuer value was set to empty and we need to prune status to default issuer",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name: "trustedIssuer1",
						},
						{
							Name:           "trustedIssuer2",
							ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
						},
					},
				},
			},
			expectedStatus: defaultServiceAccountIssuerValue,
			expectedResync: true,
		},
		{
			name: "serviceaccountissuer value changed in auth config and need to be sync to status",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "newActiveIssuer",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name: "oldActiveIssuer",
						},
					},
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "newActiveIssuer",
				},
				// old value is now being trusted for 24h
				{
					Name:           "oldActiveIssuer",
					ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
				},
			},
			expectedResync: true,
		},
		{
			name: "serviceaccountissuer value changed to a value that was previously used",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "newActiveIssuer",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name: "oldActiveIssuer",
						},
						{
							Name:           "newActiveIssuer",
							ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
						},
					},
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "newActiveIssuer",
				},
				{
					Name:           "oldActiveIssuer",
					ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
				},
			},
			expectedResync: true,
		},
		{
			name: "no active serviceaccountissuer is found in status",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "oldIssuer1",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name:           "oldIssuer1",
							ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
						},
						{
							Name:           "oldIssuer2",
							ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
						},
					},
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "oldIssuer1",
				},
				{
					Name:           "oldIssuer2",
					ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
				},
			},
			expectedResync: true,
		},
		{
			name: "serviceaccountissuer is set to previously used issuer that is being trusted",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "previousIssuer",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name: "currentIssuer",
						},
						{
							Name:           "previousIssuer",
							ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
						},
					},
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "previousIssuer",
				},
				{
					Name:           "currentIssuer",
					ExpirationTime: &metav1.Time{Time: nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)},
				},
			},
			expectedResync: true,
		},
		{
			name: "expired service account issuers need pruning",
			authConfig: configv1.Authentication{Spec: configv1.AuthenticationSpec{
				ServiceAccountIssuer: "activeIssuer",
			}},
			operator: operatorv1.KubeAPIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: operatorv1.KubeAPIServerStatus{
					ServiceAccountIssuers: []operatorv1.ServiceAccountIssuerStatus{
						{
							Name: "activeIssuer",
						},
						{
							Name: "oldIssuer",
							// expired 10s ago
							ExpirationTime: &metav1.Time{Time: nowFn().Add(-10 * time.Second)},
						},
					},
				},
			},
			expectedStatus: []operatorv1.ServiceAccountIssuerStatus{
				{
					Name: "activeIssuer",
				},
			},
			expectedResync: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fakeclient.NewSimpleClientset(&test.operator)
			eventRecorder := eventstesting.NewTestingEventRecorder(t)
			factoryContext := factory.NewSyncContext("test", eventRecorder)
			controller := ServiceAccountIssuerController{
				kubeAPIServerOperatorClient: client.OperatorV1().KubeAPIServers(),
				authLister:                  newFakeAuthLister(test.authConfig),
				kubeAPIserverOperatorLister: newFakeOperatorLister(test.operator),
				nowFn:                       nowFn,
			}

			err := controller.sync(context.TODO(), factoryContext)
			if test.expectedErr {
				if err == nil {
					t.Errorf("expected error, got none")
					return
				}
				t.Logf("got expected error: %v", err)
				return
			}
			if err != nil && err != factory.SyntheticRequeueError {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if test.expectedResync && err != factory.SyntheticRequeueError {
				t.Errorf("expected controller resync, got none")
				return
			}
			if !test.expectedResync && err == factory.SyntheticRequeueError {
				t.Errorf("not expected resync, but got one")
				return
			}

			operator, err := client.OperatorV1().KubeAPIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				t.Errorf("unexpected error getting config: %v", err)
			}
			t.Logf("o: %#v | %#v", operator.Status.ServiceAccountIssuers, test.expectedStatus)
			if !reflect.DeepEqual(operator.Status.ServiceAccountIssuers, test.expectedStatus) {
				t.Errorf("expected:\n%#v\n\nto match:\n%#v\n\n", test.expectedStatus, operator.Status.ServiceAccountIssuers)
			}

			// resync again, to check we are not changing anything and after first sync we are in steady
			// this triggers pruning, but no entries should be pruned if they were pruned in first sync.
			// use the updated operator object for lister
			operator, err = client.OperatorV1().KubeAPIServers().Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil || operator == nil {
				t.Errorf("unexpected error while getting kubeapiserver status: %v", err)
				return
			}
			controller.kubeAPIserverOperatorLister = newFakeOperatorLister(*operator)
			if err := controller.sync(context.TODO(), factoryContext); err != nil {
				t.Errorf("unexpected error after second sync(): %v", err)
			}
		})
	}
}
