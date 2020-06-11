package connectivitycheckcontroller

import (
	"context"
	"fmt"
	"net/url"
	"regexp"

	"github.com/ghodss/yaml"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/api/operatorcontrolplane/v1alpha1"
	operatorcontrolplaneclient "github.com/openshift/client-go/operatorcontrolplane/clientset/versioned"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourcehelper"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

type ConnectivityCheckController interface {
	factory.Controller
}

func NewConnectivityCheckController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	operatorcontrolplaneClient *operatorcontrolplaneclient.Clientset,
	recorder events.Recorder,
) ConnectivityCheckController {
	c := &connectivityCheckController{
		kubeClient:                 kubeClient,
		operatorClient:             operatorClient,
		operatorcontrolplaneClient: operatorcontrolplaneClient,
		endpointsLister:            kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Endpoints().Lister(),
		serviceLister:              kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Services().Lister(),
	}
	c.Controller = factory.New().
		WithSync(c.Sync).
		WithInformers(
			operatorClient.Informer(),
			kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Endpoints().Informer(),
			kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Services().Informer(),
		).
		ToController("ConnectivityCheckController", recorder.WithComponentSuffix("connectivity-check-controller"))
	return c
}

type connectivityCheckController struct {
	factory.Controller
	kubeClient                 kubernetes.Interface
	operatorClient             v1helpers.StaticPodOperatorClient
	operatorcontrolplaneClient *operatorcontrolplaneclient.Clientset
	endpointsLister            corev1listers.EndpointsLister
	serviceLister              corev1listers.ServiceLister
}

func (c *connectivityCheckController) Sync(ctx context.Context, syncContext factory.SyncContext) error {
	operatorSpec, _, _, err := c.operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return err
	}
	switch operatorSpec.ManagementState {
	case operatorv1.Managed:
	case operatorv1.Unmanaged:
		return nil
	case operatorv1.Removed:
		return nil
	default:
		syncContext.Recorder().Warningf("ManagementStateUnknown", "Unrecognized operator management state %q", operatorSpec.ManagementState)
		return nil
	}
	managePodNetworkConnectivityChecks(ctx, c.kubeClient, c.operatorcontrolplaneClient, operatorSpec, c.endpointsLister, c.serviceLister, syncContext.Recorder())
	return nil
}

func managePodNetworkConnectivityChecks(ctx context.Context, client kubernetes.Interface,
	operatorcontrolplaneClient *operatorcontrolplaneclient.Clientset,
	operatorSpec *operatorv1.StaticPodOperatorSpec, endpointsLister corev1listers.EndpointsLister,
	serviceLister corev1listers.ServiceLister, recorder events.Recorder) {

	var addresses []string
	// each etcd
	addresses = append(addresses, listAddressesForEtcd(operatorSpec, recorder)...)
	// oas service IP
	addresses = append(addresses, listAddressesForOpenShiftAPIServerService(serviceLister, recorder)...)
	// each oas endpoint
	addresses = append(addresses, listAddressesForOpenShiftAPIServerServiceEndpoints(endpointsLister, recorder)...)

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set{"node-role.kubernetes.io/master": ""}.AsSelector().String(),
	})
	if err != nil {
		recorder.Warningf("EndpointDetectionFailure", "failed to list master nodes: %v", err)
	}
	// create each check per static pod
	var checks []*v1alpha1.PodNetworkConnectivityCheck
	for _, node := range nodes.Items {
		staticPodName := "kube-apiserver-" + node.Name
		for _, address := range addresses {
			checkName := staticPodName + "-" + regexp.MustCompile(`[.:\[\]]+`).ReplaceAllLiteralString(address, "-")
			checks = append(checks, &v1alpha1.PodNetworkConnectivityCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      checkName,
					Namespace: operatorclient.TargetNamespace,
				},
				Spec: v1alpha1.PodNetworkConnectivityCheckSpec{
					SourcePod:      staticPodName,
					TargetEndpoint: address,
				},
			})
		}
	}

	pnccClient := operatorcontrolplaneClient.ControlplaneV1alpha1().PodNetworkConnectivityChecks(operatorclient.TargetNamespace)
	for _, check := range checks {
		_, err := pnccClient.Get(ctx, check.Name, metav1.GetOptions{})
		if err == nil {
			// already exists, skip
			continue
		}
		if apierrors.IsNotFound(err) {
			_, err = pnccClient.Create(ctx, check, metav1.CreateOptions{})
		}
		if err != nil {
			recorder.Warningf("EndpointDetectionFailure", "%s: %v", resourcehelper.FormatResourceForCLIWithNamespace(check), err)
			continue
		}
		recorder.Eventf("EndpointCheckCreated", "Created %s because it was missing.", resourcehelper.FormatResourceForCLIWithNamespace(check))
	}
}

func listAddressesForOpenShiftAPIServerService(serviceLister corev1listers.ServiceLister, recorder events.Recorder) []string {
	service, err := serviceLister.Services("openshift-apiserver").Get("api")
	if err != nil {
		recorder.Warningf("EndpointDetectionFailure", "unable to determine openshift-apiserver service endpoint: %v", err)
		return nil
	}
	if len(service.Spec.Ports) == 0 {
		return []string{fmt.Sprintf("%s:443", service.Spec.ClusterIP)}
	}
	return []string{fmt.Sprintf("%s:%d", service.Spec.ClusterIP, service.Spec.Ports[0].Port)}
}

// listAddressesForOpenShiftAPIServerServiceEndpoints returns oas api service endpoints ip
func listAddressesForOpenShiftAPIServerServiceEndpoints(endpointsLister corev1listers.EndpointsLister, recorder events.Recorder) []string {
	var results []string
	endpoints, err := endpointsLister.Endpoints("openshift-apiserver").Get("api")
	if err != nil {
		recorder.Warningf("EndpointDetectionFailure", "unable to determine openshift-apiserver endpoints: %v", err)
		return nil
	}
	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			for _, port := range subset.Ports {
				results = append(results, fmt.Sprintf("%s:%d", address.IP, port.Port))
			}
		}
	}
	return results
}

func listAddressesForEtcd(operatorSpec *operatorv1.StaticPodOperatorSpec, recorder events.Recorder) []string {
	var results []string
	var observedConfig map[string]interface{}
	if err := yaml.Unmarshal(operatorSpec.ObservedConfig.Raw, &observedConfig); err != nil {
		recorder.Warningf("EndpointDetectionFailure", "failed to unmarshal the observedConfig: %v", err)
		return nil
	}
	urls, _, err := unstructured.NestedStringSlice(observedConfig, "storageConfig", "urls")
	if err != nil {
		recorder.Warningf("EndpointDetectionFailure", "couldn't get the storage config urls from observedConfig: %v", err)
		return nil
	}
	for _, rawStorageConfigURL := range urls {
		storageConfigURL, err := url.Parse(rawStorageConfigURL)
		if err != nil {
			recorder.Warningf("EndpointDetectionFailure", "couldn't parse a storage config url from observedConfig: %v", err)
			continue
		}
		results = append(results, storageConfigURL.Host)
	}
	return results
}
