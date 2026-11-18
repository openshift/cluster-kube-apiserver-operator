package certrotationcontroller

import (
	"fmt"
	"time"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	certrotationv2 "github.com/openshift/library-go/pkg/operator/pki/certrotation"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/openshift/library-go/pkg/pki"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

// rotationPeriods carries the validity and refresh durations shared by the
// rotation specs. They are computed once per controller construction so the
// FeatureShortCertRotation feature gate and the development-cycle rotation
// speedup apply uniformly.
type rotationPeriods struct {
	monthPeriod                  time.Duration
	devRotationExceptionMonth    time.Duration
	yearPeriod                   time.Duration
	devRotationExceptionYear     time.Duration
	tenMonthPeriod               time.Duration
	devRotationExceptionTenMonth time.Duration
	foreverPeriod                time.Duration
	foreverRefreshPeriod         time.Duration
}

// rotationSpec is one row of the certificate rotation table: a signer, its
// CA bundle, and the single target the signer signs. Specs that share a
// signer and bundle (the load balancer serving certs, the
// kube-control-plane-signer clients) are grouped into one v2 Rotation by
// toV2Rotations; the v1 adapter always builds one controller per spec.
type rotationSpec struct {
	// name is the v1 controller name and the v2 target name; it keys the
	// status conditions (CertRotation_<name>_Degraded).
	name string

	signer signerSpec
	bundle bundleSpec
	target targetSpec
	cert   targetCertSpec
}

type signerSpec struct {
	namespace       string
	name            string
	validity        time.Duration
	refresh         time.Duration
	certificateName string
	metadata        certrotationv2.Metadata
}

type bundleSpec struct {
	namespace string
	name      string
	metadata  certrotationv2.Metadata
}

type targetSpec struct {
	namespace       string
	name            string
	validity        time.Duration
	refresh         time.Duration
	certificateName string
	metadata        certrotationv2.Metadata
}

// targetCertSpec describes the certificate kind of the target. Exactly one
// of user (client cert) or hostnames (serving cert) is set. For dynamic
// serving certs, hostnamesChanged is the notification channel fed by the
// dynamic hostname sources.
type targetCertSpec struct {
	user             *user.DefaultInfo
	hostnames        func() []string
	hostnamesChanged chan struct{}
}

// dynamicHostnameSources carries the dynamic serving hostname sources the
// table bridges into its serving cert specs.
type dynamicHostnameSources struct {
	serviceNetwork       *DynamicServingRotation
	externalLoadBalancer *DynamicServingRotation
	internalLoadBalancer *DynamicServingRotation
}

// rotationSpecs is the single source of truth for every certificate this
// operator rotates. Every namespace, name, validity, refresh, annotation
// value, certificate name, user identity and hostname source must be
// preserved exactly; the v1 adapter (toV1Controllers) reproduces the
// structs the pre-table code built inline.
func rotationSpecs(p rotationPeriods, dyn dynamicHostnameSources) []rotationSpec {
	return []rotationSpec{
		{
			name: "AggregatorProxyClientCert",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "aggregator-client-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for the kube-apiserver to create client certificates for aggregated apiservers to recognize as a front-proxy",
					TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.aggregator-front-proxy-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
				name:      "kube-apiserver-aggregator-client-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for aggregated apiservers to recognize kube-apiserver as front-proxy.",
					TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "aggregator-client",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate used by the kube-apiserver to communicate to aggregated apiservers.",
					TestName:                         "[sig-cli] oc adm new-project [apigroup:project.openshift.io][apigroup:authorization.openshift.io] [Suite:openshift/conformance/parallel]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.aggregator-front-proxy-client",
			},
			cert: targetCertSpec{
				user: &user.DefaultInfo{Name: "system:openshift-aggregator"},
			},
		},
		{
			name: "KubeAPIServerToKubeletClientCert",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-apiserver-to-kubelet-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for the kube-apiserver-to-kubelet-client so kubelets can recognize the kube-apiserver.",
					TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// this comes from the installer
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				validity:        p.devRotationExceptionYear,
				refresh:         p.devRotationExceptionTenMonth,
				certificateName: "kube-apiserver.kubelet-client-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-apiserver-to-kubelet-client-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for the kubelet to recognize the kube-apiserver client certificate.",
					TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "kubelet-client",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate used by the kube-apiserver to authenticate to the kubelet for requests like exec and logs.",
					TestName:                         "[sig-cli] Kubectl logs logs should be able to retrieve and filter logs  [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.kubelet-client",
			},
			cert: targetCertSpec{
				user: &user.DefaultInfo{Name: "system:kube-apiserver", Groups: []string{"kube-master"}},
			},
		},
		{
			name: "LocalhostServing",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "localhost-serving-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via localhost.",
					// LocalhostServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// this comes from the installer
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				// Given that in this case rotation will be after 8y,
				// it means we effectively do not rotate.
				validity:        p.foreverPeriod,
				refresh:         p.foreverRefreshPeriod,
				certificateName: "kube-apiserver.localhost-serving-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "localhost-serving-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "CA for recognizing the kube-apiserver when connecting via localhost.",
					// LocalhostServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "localhost-serving-cert-certkey",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Serving certificate used by the kube-apiserver to terminate requests via localhost.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.localhost-serving",
			},
			cert: targetCertSpec{
				hostnames: func() []string { return []string{"localhost", "127.0.0.1"} },
			},
		},
		{
			name: "ServiceNetworkServing",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "service-network-serving-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
					// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// this comes from the installer
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				// Given that in this case rotation will be after 8y,
				// it means we effectively do not rotate.
				validity:        p.foreverPeriod,
				refresh:         p.foreverRefreshPeriod,
				certificateName: "kube-apiserver.service-network-serving-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "service-network-serving-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "CA for recognizing the kube-apiserver when connecting via the service network (kubernetes.default.svc).",
					// ServiceNetworkServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "service-network-serving-certkey",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the service network.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via service network endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.service-network-serving",
			},
			cert: targetCertSpec{
				hostnames:        dyn.serviceNetwork.GetHostnames,
				hostnamesChanged: dyn.serviceNetwork.hostnamesChanged,
			},
		},
		{
			name: "ExternalLoadBalancerServing",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "loadbalancer-serving-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
					// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// this comes from the installer
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				// Given that in this case rotation will be after 8y,
				// it means we effectively do not rotate.
				validity:        p.foreverPeriod,
				refresh:         p.foreverRefreshPeriod,
				certificateName: "kube-apiserver.loadbalancer-serving-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "loadbalancer-serving-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
					// ExternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "external-loadbalancer-serving-certkey",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the external load balancer.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.external-loadbalancer-serving",
			},
			cert: targetCertSpec{
				hostnames:        dyn.externalLoadBalancer.GetHostnames,
				hostnamesChanged: dyn.externalLoadBalancer.hostnamesChanged,
			},
		},
		{
			// shares the loadbalancer-serving-signer and loadbalancer-serving-ca
			// with ExternalLoadBalancerServing
			name: "InternalLoadBalancerServing",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "loadbalancer-serving-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "Signer used by the kube-apiserver operator to create serving certificates for the kube-apiserver via internal and external load balancers.",
					// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// this comes from the installer
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				// Given that in this case rotation will be after 8y,
				// it means we effectively do not rotate.
				validity:        p.foreverPeriod,
				refresh:         p.foreverRefreshPeriod,
				certificateName: "kube-apiserver.loadbalancer-serving-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "loadbalancer-serving-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "CA for recognizing the kube-apiserver when connecting via the internal or external load balancers.",
					// InternalLoadBalancerServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "internal-loadbalancer-serving-certkey",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Serving certificate used by the kube-apiserver to terminate requests via the internal load balancer.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] kube-apiserver should be accessible via api-int endpoint [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.internal-loadbalancer-serving",
			},
			cert: targetCertSpec{
				hostnames:        dyn.internalLoadBalancer.GetHostnames,
				hostnamesChanged: dyn.internalLoadBalancer.hostnamesChanged,
			},
		},
		{
			name: "LocalhostRecoveryServing",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "localhost-recovery-serving-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "Signer used by the kube-apiserver to create serving certificates for the kube-apiserver via the service network.",
					// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// this comes from the installer
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				// Given that in this case rotation will be after 8y,
				// it means we effectively do not rotate.
				validity:        p.foreverPeriod,
				refresh:         p.foreverRefreshPeriod,
				certificateName: "kube-apiserver.localhost-recovery-serving-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "localhost-recovery-serving-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "CA for recognizing the kube-apiserver when connecting via the localhost recovery SNI ServerName.",
					// LocalhostRecoveryServing is not being tested directly, but this CA will be rotated when
					// other signers are updated and needs to have the same metadata set
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "localhost-recovery-serving-certkey",
				metadata: certrotationv2.Metadata{
					JiraComponent: "kube-apiserver",
					Description:   "Serving certificate used by the kube-apiserver to terminate requests via the localhost recovery SNI ServerName.",
					// This test checks that kube-apiserver can be contacted via localhost-recovery kubeconfig
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// this comes from the installer
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				// Given that in this case rotation will be after 8y,
				// it means we effectively do not rotate.
				validity:        p.foreverPeriod,
				refresh:         p.foreverRefreshPeriod,
				certificateName: "kube-apiserver.localhost-recovery-serving",
			},
			cert: targetCertSpec{
				hostnames: func() []string { return []string{"localhost-recovery"} },
			},
		},
		{
			// shares the kube-control-plane-signer and kube-control-plane-signer-ca
			// with KubeSchedulerClient, ControlPlaneNodeAdminClient and CheckEndpointsClient
			name: "KubeControllerManagerClient",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        2 * p.devRotationExceptionMonth,
				refresh:         p.devRotationExceptionMonth,
				certificateName: "kube-apiserver.control-plane-client-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
				name:      "kube-controller-manager-client-cert-key",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate used by the kube-controller-manager to authenticate to the kube-apiserver.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.kube-controller-manager-client",
			},
			cert: targetCertSpec{
				user: &user.DefaultInfo{Name: "system:kube-controller-manager"},
			},
		},
		{
			// shares the kube-control-plane-signer and kube-control-plane-signer-ca
			// with KubeControllerManagerClient, ControlPlaneNodeAdminClient and CheckEndpointsClient
			name: "KubeSchedulerClient",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        2 * p.devRotationExceptionMonth,
				refresh:         p.devRotationExceptionMonth,
				certificateName: "kube-apiserver.control-plane-client-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
				name:      "kube-scheduler-client-cert-key",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate used by the kube-scheduler to authenticate to the kube-apiserver.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.kube-scheduler-client",
			},
			cert: targetCertSpec{
				user: &user.DefaultInfo{Name: "system:kube-scheduler"},
			},
		},
		{
			// shares the kube-control-plane-signer and kube-control-plane-signer-ca
			// with KubeControllerManagerClient, KubeSchedulerClient and CheckEndpointsClient
			name: "ControlPlaneNodeAdminClient",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        2 * p.devRotationExceptionMonth,
				refresh:         p.devRotationExceptionMonth,
				certificateName: "kube-apiserver.control-plane-client-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "control-plane-node-admin-client-cert-key",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate and key for the control plane node kubeconfig",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"control-plane-node.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.control-plane-node-admin-client",
			},
			cert: targetCertSpec{
				user: &user.DefaultInfo{Name: "system:control-plane-node-admin", Groups: []string{"system:masters"}},
			},
		},
		{
			// shares the kube-control-plane-signer and kube-control-plane-signer-ca
			// with KubeControllerManagerClient, KubeSchedulerClient and ControlPlaneNodeAdminClient
			name: "CheckEndpointsClient",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        2 * p.devRotationExceptionMonth,
				refresh:         p.devRotationExceptionMonth,
				certificateName: "kube-apiserver.control-plane-client-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "kube-control-plane-signer-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for kube-apiserver to recognize the kube-controller-manager and kube-scheduler client certificates.",
					TestName:                         "[sig-apps] Deployment RollingUpdateDeployment should delete old pods and create new ones [Conformance] [Suite:openshift/conformance/parallel/minimal] [Suite:k8s]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.TargetNamespace,
				name:      "check-endpoints-client-cert-key",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate used by the network connectivity checker of the kube-apiserver.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"check-endpoints.kubeconfig\" should be present in all kube-apiserver containers [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity:        p.monthPeriod,
				refresh:         p.monthPeriod / 2,
				certificateName: "kube-apiserver.check-endpoints-client",
			},
			cert: targetCertSpec{
				user: &user.DefaultInfo{Name: "system:serviceaccount:openshift-kube-apiserver:check-endpoints"},
			},
		},
		{
			name: "NodeSystemAdminClient",
			signer: signerSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "node-system-admin-signer",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Signer for the per-master-debugging-client.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				validity: 3 * p.devRotationExceptionYear,
				// Refresh set to 80% of the validity.
				// This range is consistent with most other signers defined in this pkg.
				refresh:         3 * p.devRotationExceptionTenMonth,
				certificateName: "kube-apiserver.node-system-admin-signer",
			},
			bundle: bundleSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "node-system-admin-ca",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "CA for kube-apiserver to recognize local system:masters rendered to each master.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
			},
			target: targetSpec{
				namespace: operatorclient.OperatorNamespace,
				name:      "node-system-admin-client",
				metadata: certrotationv2.Metadata{
					JiraComponent:                    "kube-apiserver",
					Description:                      "Client certificate (system:masters) placed on each master to allow communication to kube-apiserver for debugging.",
					TestName:                         "[Conformance][sig-api-machinery][Feature:APIServer] local kubeconfig \"localhost-recovery.kubeconfig\" should be present on all masters and work [apigroup:config.openshift.io] [Suite:openshift/conformance/parallel/minimal]",
					AutoRegenerateAfterOfflineExpiry: "https://github.com/openshift/cluster-kube-apiserver-operator/pull/1631",
				},
				// This needs to live longer then control plane certs so there is high chance that if a cluster breaks
				// because of expired certs these are still valid to use for collecting data using localhost-recovery
				// endpoint with long lived serving certs for localhost.
				validity: 2 * p.devRotationExceptionYear,
				// We rotate sooner so certs are always valid for 90 days (30 days more then kube-control-plane-signer)
				refresh:         p.monthPeriod,
				certificateName: "kube-apiserver.node-system-admin-client",
			},
			cert: targetCertSpec{
				user: &user.DefaultInfo{
					Name:   "system:admin",
					Groups: []string{"system:masters"},
				},
			},
		},
	}
}

// toV1Annotations converts the table's metadata into v1's
// AdditionalAnnotations. The two types are field-compatible by contract.
func toV1Annotations(m certrotationv2.Metadata) certrotation.AdditionalAnnotations {
	return certrotation.AdditionalAnnotations{
		JiraComponent:                    m.JiraComponent,
		Description:                      m.Description,
		TestName:                         m.TestName,
		AutoRegenerateAfterOfflineExpiry: m.AutoRegenerateAfterOfflineExpiry,
	}
}

// v1ControllerDeps carries the clients and informers the v1 controllers
// need; they are shared by every spec.
type v1ControllerDeps struct {
	kubeClient                 kubernetes.Interface
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces
	eventRecorder              events.Recorder
	reporter                   *certrotation.StaticPodConditionStatusReporter
}

// toV1Controllers builds one v1 certrotation controller per spec, exactly
// as the pre-table code did. This is the feature-gate-off path and the
// RunOnce path.
func toV1Controllers(
	specs []rotationSpec,
	deps v1ControllerDeps,
	refreshOnlyWhenExpired bool,
	pkiConfigProvider pki.ConfigProvider,
) ([]factory.Controller, error) {
	var controllers []factory.Controller
	for i := range specs {
		spec := specs[i]
		signingCASecret, caBundleConfigMap, targetSecret := toV1Structs(spec, refreshOnlyWhenExpired, pkiConfigProvider)
		signingCASecret.Informer = deps.kubeInformersForNamespaces.InformersFor(spec.signer.namespace).Core().V1().Secrets()
		signingCASecret.Lister = deps.kubeInformersForNamespaces.InformersFor(spec.signer.namespace).Core().V1().Secrets().Lister()
		signingCASecret.Client = deps.kubeClient.CoreV1()
		signingCASecret.EventRecorder = deps.eventRecorder
		caBundleConfigMap.Informer = deps.kubeInformersForNamespaces.InformersFor(spec.bundle.namespace).Core().V1().ConfigMaps()
		caBundleConfigMap.Lister = deps.kubeInformersForNamespaces.InformersFor(spec.bundle.namespace).Core().V1().ConfigMaps().Lister()
		caBundleConfigMap.Client = deps.kubeClient.CoreV1()
		caBundleConfigMap.EventRecorder = deps.eventRecorder
		targetSecret.Informer = deps.kubeInformersForNamespaces.InformersFor(spec.target.namespace).Core().V1().Secrets()
		targetSecret.Lister = deps.kubeInformersForNamespaces.InformersFor(spec.target.namespace).Core().V1().Secrets().Lister()
		targetSecret.Client = deps.kubeClient.CoreV1()
		targetSecret.EventRecorder = deps.eventRecorder
		controller := certrotation.NewCertRotationController(
			spec.name,
			signingCASecret,
			caBundleConfigMap,
			targetSecret,
			deps.eventRecorder,
			deps.reporter,
		)
		controllers = append(controllers, controller)
	}
	return controllers, nil
}

// toV1Structs builds the v1 rotation structs of one spec, excluding the
// informer, lister, client and recorder wiring, which is per-controller.
// The output must stay identical to what the pre-table code built inline.
func toV1Structs(
	spec rotationSpec,
	refreshOnlyWhenExpired bool,
	pkiConfigProvider pki.ConfigProvider,
) (certrotation.RotatedSigningCASecret, certrotation.CABundleConfigMap, certrotation.RotatedSelfSignedCertKeySecret) {
	return certrotation.RotatedSigningCASecret{
			Namespace:              spec.signer.namespace,
			Name:                   spec.signer.name,
			AdditionalAnnotations:  toV1Annotations(spec.signer.metadata),
			Validity:               spec.signer.validity,
			Refresh:                spec.signer.refresh,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertificateName:        spec.signer.certificateName,
			PKIConfigProvider:      pkiConfigProvider,
		},
		certrotation.CABundleConfigMap{
			Namespace:              spec.bundle.namespace,
			Name:                   spec.bundle.name,
			AdditionalAnnotations:  toV1Annotations(spec.bundle.metadata),
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace:              spec.target.namespace,
			Name:                   spec.target.name,
			AdditionalAnnotations:  toV1Annotations(spec.target.metadata),
			Validity:               spec.target.validity,
			Refresh:                spec.target.refresh,
			RefreshOnlyWhenExpired: refreshOnlyWhenExpired,
			CertCreator:            toV1CertCreator(spec.cert),
			CertificateName:        spec.target.certificateName,
			PKIConfigProvider:      pkiConfigProvider,
		}
}

func toV1CertCreator(cert targetCertSpec) certrotation.TargetCertCreator {
	if cert.user != nil {
		return &certrotation.ClientRotation{UserInfo: cert.user}
	}
	return &certrotation.ServingRotation{
		Hostnames:        cert.hostnames,
		HostnamesChanged: cert.hostnamesChanged,
	}
}

// hostnameSourceFunc adapts a hostname function to the v2 HostnameSource
// interface. It bridges CKAO's DynamicServingRotation (whose GetHostnames
// method is consulted on every reconcile) into v2 serving certs without
// rewriting the event handlers and queues that feed it. Hostname changes are
// picked up by the v2 controller's resync.
type hostnameSourceFunc func() []string

func (f hostnameSourceFunc) Hostnames() []string {
	return f()
}

// toV2Rotations groups the specs by signer (and its bundle) into v2
// Rotations. Specs sharing a signer become multiple Targets of a single
// Rotation, so exactly one reconcile loop owns each signer secret.
func toV2Rotations(specs []rotationSpec) ([]certrotationv2.Rotation, error) {
	var rotations []certrotationv2.Rotation
	indexBySigner := map[string]int{}
	for i := range specs {
		spec := specs[i]
		key := spec.signer.namespace + "/" + spec.signer.name
		index, found := indexBySigner[key]
		if !found {
			rotations = append(rotations, certrotationv2.Rotation{
				Signer: certrotationv2.SignerConfig{
					Location: certrotationv2.Location{
						Namespace: spec.signer.namespace,
						Name:      spec.signer.name,
					},
					CertificateName: spec.signer.certificateName,
					Validity:        spec.signer.validity,
					Refresh:         spec.signer.refresh,
					Metadata:        spec.signer.metadata,
				},
				Bundle: certrotationv2.BundleConfig{
					Location: certrotationv2.Location{
						Namespace: spec.bundle.namespace,
						Name:      spec.bundle.name,
					},
					Metadata: spec.bundle.metadata,
				},
			})
			indexBySigner[key] = len(rotations) - 1
			index = len(rotations) - 1
		} else {
			// every spec sharing a signer must agree on the bundle; the v1
			// code built identical bundle structs for them
			if rotations[index].Bundle.Location != (certrotationv2.Location{Namespace: spec.bundle.namespace, Name: spec.bundle.name}) {
				return nil, fmt.Errorf("signer %s shared by specs with different bundles", key)
			}
		}
		rotations[index].Targets = append(rotations[index].Targets, certrotationv2.Target{
			Name: spec.name,
			Location: certrotationv2.Location{
				Namespace: spec.target.namespace,
				Name:      spec.target.name,
			},
			CertificateName: spec.target.certificateName,
			Validity:        spec.target.validity,
			Refresh:         spec.target.refresh,
			Metadata:        spec.target.metadata,
			Cert:            toV2TargetCert(spec.cert),
		})
	}
	return rotations, nil
}

func toV2TargetCert(cert targetCertSpec) certrotationv2.TargetCert {
	if cert.user != nil {
		return certrotationv2.ClientCert{User: cert.user}
	}
	return certrotationv2.ServingCert{
		Hostnames: hostnameSourceFunc(cert.hostnames),
	}
}
