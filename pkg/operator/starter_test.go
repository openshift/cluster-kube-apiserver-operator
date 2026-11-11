package operator

import (
	"context"
	"testing"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	condition "github.com/openshift/library-go/pkg/operator/condition"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestOIDCAuthConfigReadinessCheck(t *testing.T) {
	tests := []struct {
		name       string
		directOIDC bool
		authConfig map[string]string
		wantReady  bool
		wantError  bool
	}{
		{name: "no authentication config referenced", wantReady: true},
		{
			name:       "referenced authentication config exists",
			directOIDC: true,
			authConfig: map[string]string{"auth-config.json": `{"apiVersion":"apiserver.config.k8s.io/v1beta1","kind":"AuthenticationConfiguration"}`},
			wantReady:  true,
		},
		{name: "referenced authentication config missing", directOIDC: true, wantError: true},
		{name: "referenced authentication config empty", directOIDC: true, authConfig: map[string]string{"auth-config.json": ""}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := "apiServerArguments: {}\n"
			if tt.directOIDC {
				config = "apiServerArguments:\n  authentication-config:\n  - /etc/kubernetes/static-pod-resources/configmaps/auth-config/auth-config.json\n"
			}
			objects := []runtime.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "config-1", Namespace: operatorclient.TargetNamespace},
				Data:       map[string]string{"config.yaml": config},
			}}
			if tt.authConfig != nil {
				objects = append(objects, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "auth-config-1", Namespace: operatorclient.TargetNamespace},
					Data:       tt.authConfig,
				})
			}

			ready, err := oidcAuthConfigReadinessCheck(context.Background(), fake.NewSimpleClientset(objects...), 1)
			if ready != tt.wantReady || (err != nil) != tt.wantError {
				t.Fatalf("got ready=%t, err=%v; want ready=%t, error=%t", ready, err, tt.wantReady, tt.wantError)
			}
		})
	}
}

func TestNewDegradedInertia(t *testing.T) {
	inertia := newDegradedInertia()

	tests := []struct {
		conditionType string
		expected      time.Duration
	}{
		{
			conditionType: condition.NodeControllerDegradedConditionType,
			expected:      10 * time.Minute,
		},
		{
			conditionType: condition.StaticPodsDegradedConditionType,
			expected:      10 * time.Minute,
		},
		{
			conditionType: condition.GuardControllerDegradedConditionType,
			expected:      10 * time.Minute,
		},
		{
			conditionType: "TargetConfigControllerDegraded",
			expected:      2 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.conditionType, func(t *testing.T) {
			got := inertia(operatorv1.OperatorCondition{Type: tt.conditionType})
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
