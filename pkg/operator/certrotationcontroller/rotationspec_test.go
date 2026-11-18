package certrotationcontroller

import (
	"reflect"
	"testing"
	"time"

	"k8s.io/apiserver/pkg/authentication/user"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	certrotationv2 "github.com/openshift/library-go/pkg/operator/pki/certrotation"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

// testPeriods mirrors the durations computed in newCertRotationController
// with no feature gates enabled: rotationDay is 24h/60 for the development
// cycle, and the dev rotation exceptions use the full 24h day.
func testPeriods() rotationPeriods {
	rotationDay := (24 * time.Hour) / 60
	devRotationExceptionDay := 24 * time.Hour
	return rotationPeriods{
		monthPeriod:                  30 * rotationDay,
		devRotationExceptionMonth:    30 * devRotationExceptionDay,
		yearPeriod:                   365 * rotationDay,
		devRotationExceptionYear:     365 * devRotationExceptionDay,
		tenMonthPeriod:               292 * rotationDay,
		devRotationExceptionTenMonth: 292 * devRotationExceptionDay,
		foreverPeriod:                10 * 365 * 24 * time.Hour,
		foreverRefreshPeriod:         8 * 365 * 24 * time.Hour,
	}
}

func testSpecs(t *testing.T) ([]rotationSpec, dynamicHostnameSources) {
	t.Helper()
	dyn := dynamicHostnameSources{
		serviceNetwork:       &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},
		externalLoadBalancer: &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},
		internalLoadBalancer: &DynamicServingRotation{hostnamesChanged: make(chan struct{}, 10)},
	}
	return rotationSpecs(testPeriods(), dyn), dyn
}

func specByName(t *testing.T, specs []rotationSpec, name string) rotationSpec {
	t.Helper()
	for i := range specs {
		if specs[i].name == name {
			return specs[i]
		}
	}
	t.Fatalf("spec %q not found", name)
	return rotationSpec{}
}

// wantTargetSecret describes the expected v1 target secret. hostnames and
// hostnamesChanged describe the expected serving cert creator (exactly one
// of userInfo / hostnames is set); func values cannot be compared with
// reflect.DeepEqual, so they are checked by invocation and channel identity.
type wantTargetSecret struct {
	namespace           string
	name                string
	annotations         certrotation.AdditionalAnnotations
	validity            time.Duration
	refresh             time.Duration
	certificateName     string
	userInfo            *user.DefaultInfo
	hostnames           []string
	hostnamesChangedNil bool
	dynamicRotation     *DynamicServingRotation
}

func compareTargetSecret(t *testing.T, got certrotation.RotatedSelfSignedCertKeySecret, want wantTargetSecret) {
	t.Helper()

	if got.Namespace != want.namespace {
		t.Errorf("target namespace: got %q, want %q", got.Namespace, want.namespace)
	}
	if got.Name != want.name {
		t.Errorf("target name: got %q, want %q", got.Name, want.name)
	}
	if !reflect.DeepEqual(got.AdditionalAnnotations, want.annotations) {
		t.Errorf("target annotations:\n got: %#v\nwant: %#v", got.AdditionalAnnotations, want.annotations)
	}
	if got.Validity != want.validity {
		t.Errorf("target validity: got %v, want %v", got.Validity, want.validity)
	}
	if got.Refresh != want.refresh {
		t.Errorf("target refresh: got %v, want %v", got.Refresh, want.refresh)
	}
	if got.CertificateName != want.certificateName {
		t.Errorf("target certificate name: got %q, want %q", got.CertificateName, want.certificateName)
	}
	if !got.RefreshOnlyWhenExpired {
		t.Errorf("target RefreshOnlyWhenExpired: got false, want true")
	}

	switch creator := got.CertCreator.(type) {
	case *certrotation.ClientRotation:
		if want.userInfo == nil {
			t.Fatalf("cert creator: got ClientRotation, want ServingRotation")
		}
		if !reflect.DeepEqual(creator.UserInfo, want.userInfo) {
			t.Errorf("client user info:\n got: %#v\nwant: %#v", creator.UserInfo, want.userInfo)
		}
	case *certrotation.ServingRotation:
		if want.userInfo != nil {
			t.Fatalf("cert creator: got ServingRotation, want ClientRotation")
		}
		if !reflect.DeepEqual(creator.Hostnames(), want.hostnames) {
			t.Errorf("serving hostnames: got %v, want %v", creator.Hostnames(), want.hostnames)
		}
		if want.hostnamesChangedNil {
			if creator.HostnamesChanged != nil {
				t.Errorf("serving HostnamesChanged: expected nil for a static hostname source")
			}
		} else if want.dynamicRotation != nil {
			if creator.HostnamesChanged != want.dynamicRotation.hostnamesChanged {
				t.Errorf("serving HostnamesChanged: expected the dynamic rotation's channel")
			}
		} else {
			t.Errorf("serving HostnamesChanged: expected either nil or a dynamic rotation")
		}
	default:
		t.Fatalf("unexpected cert creator type %T", got.CertCreator)
	}
}

// TestToV1Structs asserts that the v1 structs built from the table match
// the values the pre-table code built inline, for representative specs: a
// client cert, a shared-signer serving cert, and a forever-validity serving
// cert.
func TestToV1Structs(t *testing.T) {
	periods := testPeriods()
	specs, dyn := testSpecs(t)

	testCases := []struct {
		name       string
		specName   string
		wantSigner certrotation.RotatedSigningCASecret
		wantBundle certrotation.CABundleConfigMap
		wantTarget wantTargetSecret
	}{
		{
			name:     "client cert (AggregatorProxyClientCert)",
			specName: "AggregatorProxyClientCert",
			wantSigner: certrotation.RotatedSigningCASecret{
				Namespace: operatorclient.OperatorNamespace,
				Name:      "aggregator-client-signer",
				AdditionalAnnotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for the kube-apiserver to create client certificates for aggregated apiservers to recognize as a front-proxy",
					TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				Validity:               periods.monthPeriod,
				Refresh:                periods.monthPeriod / 2,
				RefreshOnlyWhenExpired: true,
				CertificateName:        "kube-apiserver.aggregator-front-proxy-signer",
			},
			wantBundle: certrotation.CABundleConfigMap{
				Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
				Name:      "kube-apiserver-aggregator-client-ca",
				AdditionalAnnotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for aggregated apiservers to recognize kube-apiserver as front-proxy.",
					TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				RefreshOnlyWhenExpired: true,
			},
			wantTarget: wantTargetSecret{
				namespace: operatorclient.TargetNamespace,
				name:      "aggregator-client",
				annotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate used by the kube-apiserver to communicate to aggregated apiservers.",
					TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        periods.monthPeriod,
				refresh:         periods.monthPeriod / 2,
				certificateName: "kube-apiserver.aggregator-front-proxy-client",
				userInfo:        &user.DefaultInfo{Name: "system:openshift-aggregator"},
			},
		},
		{
			name:     "shared-signer serving cert (ExternalLoadBalancerServing)",
			specName: "ExternalLoadBalancerServing",
			wantSigner: certrotation.RotatedSigningCASecret{
				Namespace: operatorclient.OperatorNamespace,
				Name:      "loadbalancer-serving-signer",
				AdditionalAnnotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				Validity:               periods.foreverPeriod,
				Refresh:                periods.foreverRefreshPeriod,
				RefreshOnlyWhenExpired: true,
				CertificateName:        "kube-apiserver.loadbalancer-serving-signer",
			},
			wantBundle: certrotation.CABundleConfigMap{
				Namespace: operatorclient.OperatorNamespace,
				Name:      "loadbalancer-serving-ca",
				AdditionalAnnotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				RefreshOnlyWhenExpired: true,
			},
			wantTarget: wantTargetSecret{
				namespace: operatorclient.TargetNamespace,
				name:      "external-loadbalancer-serving-certkey",
				annotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the external load balancer.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        periods.monthPeriod,
				refresh:         periods.monthPeriod / 2,
				certificateName: "kube-apiserver.external-loadbalancer-serving",
				dynamicRotation: dyn.externalLoadBalancer,
			},
		},
		{
			name:     "forever-validity serving cert (LocalhostRecoveryServing)",
			specName: "LocalhostRecoveryServing",
			wantSigner: certrotation.RotatedSigningCASecret{
				Namespace: operatorclient.OperatorNamespace,
				Name:      "localhost-recovery-serving-signer",
				AdditionalAnnotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				Validity:               periods.foreverPeriod,
				Refresh:                periods.foreverRefreshPeriod,
				RefreshOnlyWhenExpired: true,
				CertificateName:        "kube-apiserver.localhost-recovery-serving-signer",
			},
			wantBundle: certrotation.CABundleConfigMap{
				Namespace: operatorclient.OperatorNamespace,
				Name:      "localhost-recovery-serving-ca",
				AdditionalAnnotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for recognizing the kube-apiserver when connecting via the localhost recovery SNI ServerName.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				RefreshOnlyWhenExpired: true,
			},
			wantTarget: wantTargetSecret{
				namespace: operatorclient.TargetNamespace,
				name:      "localhost-recovery-serving-certkey",
				annotations: certrotation.AdditionalAnnotations{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the localhost recovery SNI ServerName.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:            periods.foreverPeriod,
				refresh:             periods.foreverRefreshPeriod,
				certificateName:     "kube-apiserver.localhost-recovery-serving",
				hostnames:           []string{"localhost-recovery"},
				hostnamesChangedNil: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spec := specByName(t, specs, tc.specName)
			signingCASecret, caBundleConfigMap, targetSecret := toV1Structs(spec, true, nil)

			if !reflect.DeepEqual(signingCASecret, tc.wantSigner) {
				t.Errorf("signing CA secret mismatch:\n got: %#v\nwant: %#v", signingCASecret, tc.wantSigner)
			}
			if !reflect.DeepEqual(caBundleConfigMap, tc.wantBundle) {
				t.Errorf("CA bundle configmap mismatch:\n got: %#v\nwant: %#v", caBundleConfigMap, tc.wantBundle)
			}
			compareTargetSecret(t, targetSecret, tc.wantTarget)
		})
	}
}

// TestToV2RotationsGrouping asserts the v2 grouping: specs sharing a signer
// and bundle become multiple targets of one Rotation, and every other spec
// gets its own Rotation.
func TestToV2RotationsGrouping(t *testing.T) {
	specs, _ := testSpecs(t)

	rotations, err := toV2Rotations(specs)
	if err != nil {
		t.Fatal(err)
	}

	if len(rotations) != 8 {
		t.Errorf("expected 8 rotations, got %d", len(rotations))
	}

	testCases := []struct {
		name        string
		signerName  string
		bundleName  string
		targetNames []string
	}{
		{
			name:        "loadbalancer signer groups both load balancer serving certs",
			signerName:  "loadbalancer-serving-signer",
			bundleName:  "loadbalancer-serving-ca",
			targetNames: []string{"ExternalLoadBalancerServing", "InternalLoadBalancerServing"},
		},
		{
			name:        "kube-control-plane signer groups all four clients",
			signerName:  "kube-control-plane-signer",
			bundleName:  "kube-control-plane-signer-ca",
			targetNames: []string{"KubeControllerManagerClient", "KubeSchedulerClient", "ControlPlaneNodeAdminClient", "CheckEndpointsClient"},
		},
		{
			name:        "aggregator signer stands alone",
			signerName:  "aggregator-client-signer",
			bundleName:  "kube-apiserver-aggregator-client-ca",
			targetNames: []string{"AggregatorProxyClientCert"},
		},
		{
			name:        "node-system-admin signer stands alone",
			signerName:  "node-system-admin-signer",
			bundleName:  "node-system-admin-ca",
			targetNames: []string{"NodeSystemAdminClient"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var found *certrotationv2.Rotation
			for i := range rotations {
				if rotations[i].Signer.Name == tc.signerName {
					found = &rotations[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("rotation for signer %q not found", tc.signerName)
			}
			if found.Bundle.Name != tc.bundleName {
				t.Errorf("rotation for signer %q: bundle name: got %q, want %q", tc.signerName, found.Bundle.Name, tc.bundleName)
			}
			if len(found.Targets) != len(tc.targetNames) {
				t.Fatalf("rotation for signer %q: expected %d targets, got %d", tc.signerName, len(tc.targetNames), len(found.Targets))
			}
			for i, wantName := range tc.targetNames {
				if found.Targets[i].Name != wantName {
					t.Errorf("rotation for signer %q: target %d: expected %q, got %q", tc.signerName, i, wantName, found.Targets[i].Name)
				}
			}
			if err := found.Validate(); err != nil {
				t.Errorf("rotation for signer %q does not validate: %v", tc.signerName, err)
			}
		})
	}
}

// TestRotationSpecsPreservesSpecCount guards against specs being dropped
// from the table: 12 specs must build 12 v1 controllers.
func TestRotationSpecsPreservesSpecCount(t *testing.T) {
	specs, _ := testSpecs(t)
	if len(specs) != 12 {
		t.Errorf("expected 12 rotation specs, got %d", len(specs))
	}
}
