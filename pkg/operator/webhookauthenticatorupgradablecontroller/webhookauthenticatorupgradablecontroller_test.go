package webhookauthenticatorupgradablecontroller

import (
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestNewUpgradeableCondition(t *testing.T) {
	tests := []struct {
		name string

		authenticatorconfig *configv1.WebhookTokenAuthenticator
		expected            operatorv1.OperatorCondition
	}{
		{
			name: "default",
			expected: operatorv1.OperatorCondition{
				Reason: "NoWebhookTokenAuthenticatorConfigured",
				Status: "True",
				Type:   "AuthenticationConfigUpgradeable",
			},
		},
		{
			name:                "webhooktokenauthenticator is not nil",
			authenticatorconfig: &configv1.WebhookTokenAuthenticator{},
			expected: operatorv1.OperatorCondition{
				Reason:  "WebhookTokenAuthenticatorConfigured",
				Status:  "False",
				Type:    "AuthenticationConfigUpgradeable",
				Message: "upgrades are not allowed when authentication.config/cluster .spec.WebhookTokenAuthenticator is set",
			},
		},
		{
			name: "secret ref configured",
			authenticatorconfig: &configv1.WebhookTokenAuthenticator{
				KubeConfig: configv1.SecretNameReference{
					Name: "somename",
				},
			},
			expected: operatorv1.OperatorCondition{
				Reason:  "WebhookTokenAuthenticatorConfigured",
				Status:  "False",
				Type:    "AuthenticationConfigUpgradeable",
				Message: "upgrades are not allowed when authentication.config/cluster .spec.WebhookTokenAuthenticator is set",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := newUpgradeableCondition(&configv1.Authentication{
				Spec: configv1.AuthenticationSpec{
					WebhookTokenAuthenticator: test.authenticatorconfig,
				},
			})

			if !reflect.DeepEqual(test.expected, actual) {
				t.Fatal(spew.Sdump(actual))
			}
		})
	}
}
