package connectivitycheckcontroller

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/ghodss/yaml"
	"github.com/openshift/api/operatorcontrolplane/v1alpha1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	operatorcontrolplaneclient "github.com/openshift/client-go/operatorcontrolplane/clientset/versioned"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/connectivitycheckcontroller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	v1 "k8s.io/api/core/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type KubeAPIServerConnectivityCheckController interface {
	connectivitycheckcontroller.ConnectivityCheckController
}

func NewKubeAPIServerConnectivityCheckController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	apiextensionsClient *apiextensionsclient.Clientset,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	operatorcontrolplaneClient *operatorcontrolplaneclient.Clientset,
	configInformers configinformers.SharedInformerFactory,
	apiextensionsInformers apiextensionsinformers.SharedInformerFactory,
	recorder events.Recorder,
) KubeAPIServerConnectivityCheckController {
	c := kubeAPIServerConnectivityCheckController{
		ConnectivityCheckController: connectivitycheckcontroller.NewConnectivityCheckController(
			operatorclient.TargetNamespace,
			operatorClient,
			operatorcontrolplaneClient,
			apiextensionsClient,
			apiextensionsInformers,
			[]factory.Informer{
				kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Endpoints().Informer(),
				kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Services().Informer(),
				configInformers.Config().V1().Infrastructures().Informer(),
			},
			recorder,
		),
	}
	generator := &connectivityCheckTemplateProvider{
		kubeClient:           kubeClient,
		operatorClient:       operatorClient,
		endpointsLister:      kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Endpoints().Lister(),
		serviceLister:        kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Services().Lister(),
		nodeLister:           kubeInformersForNamespaces.InformersFor("").Core().V1().Nodes().Lister(),
		infrastructureLister: configInformers.Config().V1().Infrastructures().Lister(),
	}
	return c.WithPodNetworkConnectivityCheckFn(generator.generate)
}

type kubeAPIServerConnectivityCheckController struct {
	connectivitycheckcontroller.ConnectivityCheckController
}

type connectivityCheckTemplateProvider struct {
	kubeClient           kubernetes.Interface
	operatorClient       v1helpers.OperatorClient
	endpointsLister      corev1listers.EndpointsLister
	serviceLister        corev1listers.ServiceLister
	nodeLister           corev1listers.NodeLister
	infrastructureLister configv1listers.InfrastructureLister
}

func (c *connectivityCheckTemplateProvider) generate(ctx context.Context, syncContext factory.SyncContext) ([]*v1alpha1.PodNetworkConnectivityCheck, error) {
	var templates []*v1alpha1.PodNetworkConnectivityCheck
	// each storage endpoint
	etcdEndpoints, err := c.getTemplatesForEtcdEndpoints(syncContext)
	if err != nil {
		syncContext.Recorder().Warningf("EndpointDetectionFailure", "error detecting etcd server endpoints: %v", err)
	}
	templates = append(templates, etcdEndpoints...)

	// oas service IP
	oasServiceIP, err := c.getTemplatesForOpenShiftAPIServerService(syncContext)
	if err != nil {
		syncContext.Recorder().Warningf("EndpointDetectionFailure", "error detecting openshift-apiserver service: %v", err)
	}
	templates = append(templates, oasServiceIP...)

	// each oas endpoint
	oasEndpointIPs, err := c.getTemplatesForOpenShiftAPIServerEndpoints(syncContext)
	if err != nil {
		syncContext.Recorder().Warningf("EndpointDetectionFailure", "error detecting openshift-apiserver service endpoints: %v", err)
	}
	templates = append(templates, oasEndpointIPs...)

	// api load balancer endpoints
	loadBalancerEndpoints, err := c.getTemplatesForApiLoadBalancerEndpoints(syncContext)
	if err != nil {
		syncContext.Recorder().Warningf("EndpointDetectionFailure", "error detecting api load balancer endpoints: %v", err)
	}
	templates = append(templates, loadBalancerEndpoints...)

	nodes, err := c.kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set{"node-role.kubernetes.io/master": ""}.AsSelector().String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list master nodes: %w", err)
	}

	// create each check per static pod
	var checks []*v1alpha1.PodNetworkConnectivityCheck
	for _, node := range nodes.Items {
		staticPodName := "kube-apiserver-" + node.Name
		for _, template := range templates {
			check := template.DeepCopy()
			connectivitycheckcontroller.WithSource(staticPodName)(check)
			check.Spec.SourcePod = staticPodName
			checks = append(checks, check)
		}
	}

	return checks, nil
}

func (c *connectivityCheckTemplateProvider) getTemplatesForOpenShiftAPIServerService(syncContext factory.SyncContext) ([]*v1alpha1.PodNetworkConnectivityCheck, error) {
	var templates []*v1alpha1.PodNetworkConnectivityCheck
	ips, err := c.listAddressesForOpenShiftAPIServerService(syncContext)
	if err != nil {
		return nil, err
	}
	for _, address := range ips {
		templates = append(templates, connectivitycheckcontroller.NewPodNetworkConnectivityCheckTemplate(address,
			operatorclient.TargetNamespace,
			withTarget("openshift-apiserver-service", "cluster"),
		))
	}
	return templates, nil
}

func (c *connectivityCheckTemplateProvider) listAddressesForOpenShiftAPIServerService(syncContext factory.SyncContext) ([]string, error) {
	service, err := c.serviceLister.Services("openshift-apiserver").Get("api")
	if err != nil {
		return nil, err
	}
	for _, port := range service.Spec.Ports {
		if port.TargetPort.IntValue() == 6443 {
			return []string{net.JoinHostPort(service.Spec.ClusterIP, strconv.Itoa(int(port.Port)))}, nil
		}
	}
	return []string{net.JoinHostPort(service.Spec.ClusterIP, "443")}, nil
}

func (c *connectivityCheckTemplateProvider) getTemplatesForOpenShiftAPIServerEndpoints(syncContext factory.SyncContext) ([]*v1alpha1.PodNetworkConnectivityCheck, error) {
	var templates []*v1alpha1.PodNetworkConnectivityCheck
	addresses, err := c.listAddressesForOpenShiftAPIServerServiceEndpoints(syncContext)
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		targetEndpoint := net.JoinHostPort(address.hostName, address.port)
		templates = append(templates, connectivitycheckcontroller.NewPodNetworkConnectivityCheckTemplate(targetEndpoint, operatorclient.TargetNamespace, withTarget("openshift-apiserver-endpoint", address.nodeName)))
	}
	return templates, nil
}

// listAddressesForOpenShiftAPIServerServiceEndpoints returns oas api service endpoints ip
func (c *connectivityCheckTemplateProvider) listAddressesForOpenShiftAPIServerServiceEndpoints(syncContext factory.SyncContext) ([]endpointInfo, error) {
	endpoints, err := c.endpointsLister.Endpoints("openshift-apiserver").Get("api")
	if err != nil {
		return nil, err
	}
	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Ports) == 0 {
		return nil, fmt.Errorf("no openshift-apiserver api endpoints found")
	}
	port := strconv.Itoa(int(endpoints.Subsets[0].Ports[0].Port))
	var results []endpointInfo
	for _, address := range endpoints.Subsets[0].Addresses {
		results = append(results, endpointInfo{
			hostName: address.IP,
			port:     port,
			nodeName: *address.NodeName,
		})
	}
	return results, nil
}

func (c *connectivityCheckTemplateProvider) getTemplatesForEtcdEndpoints(syncContext factory.SyncContext) ([]*v1alpha1.PodNetworkConnectivityCheck, error) {
	var templates []*v1alpha1.PodNetworkConnectivityCheck
	endpointInfos, err := c.listAddressesForEtcdServerEndpoints(syncContext)
	if err != nil {
		syncContext.Recorder().Warningf("EndpointDetectionFailure", "error detecting etcd server endpoints: %v", err)
		return nil, err
	}
	for _, endpointInfo := range endpointInfos {
		templates = append(templates, connectivitycheckcontroller.NewPodNetworkConnectivityCheckTemplate(
			net.JoinHostPort(endpointInfo.hostName, endpointInfo.port),
			operatorclient.TargetNamespace,
			withTarget("etcd-server", endpointInfo.nodeName),
			connectivitycheckcontroller.WithTlsClientCert("etcd-client"),
		))
	}
	return templates, nil
}

func (c *connectivityCheckTemplateProvider) listAddressesForEtcdServerEndpoints(syncContext factory.SyncContext) ([]endpointInfo, error) {
	operatorSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return nil, fmt.Errorf("failed to get the operatorSpec: %w", err)
	}

	var results []endpointInfo
	var observedConfig map[string]interface{}
	if err := yaml.Unmarshal(operatorSpec.ObservedConfig.Raw, &observedConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal the observedConfig: %w", err)
	}
	urls, _, err := unstructured.NestedStringSlice(observedConfig, "apiServerArguments", "etcd-servers")
	if err != nil {
		return nil, fmt.Errorf("couldn't get the etcd server urls from observedConfig: %w", err)
	}
	for _, rawStorageConfigURL := range urls {
		storageConfigURL, err := url.Parse(rawStorageConfigURL)
		if err != nil {
			syncContext.Recorder().Warningf("EndpointDetectionFailure", "couldn't parse an etcd server url from observedConfig: %v", err)
			continue
		}
		node, err := c.findNodeForInternalIP(storageConfigURL.Hostname())
		if err != nil {
			syncContext.Recorder().Warningf("EndpointDetectionFailure", "unable to determine node for etcd server: %v", err)
			continue
		}
		results = append(results, endpointInfo{
			hostName: storageConfigURL.Hostname(),
			port:     storageConfigURL.Port(),
			nodeName: node.Name,
		})
	}
	return results, nil
}

func (c *connectivityCheckTemplateProvider) getTemplatesForApiLoadBalancerEndpoints(syncContext factory.SyncContext) ([]*v1alpha1.PodNetworkConnectivityCheck, error) {
	var templates []*v1alpha1.PodNetworkConnectivityCheck
	infrastructure, err := c.infrastructureLister.Get("cluster")
	if err != nil {
		return nil, err
	}
	apiUrl, err := url.Parse(infrastructure.Status.APIServerURL)
	if err != nil {
		return nil, err
	}
	templates = append(templates, connectivitycheckcontroller.NewPodNetworkConnectivityCheckTemplate(apiUrl.Host, operatorclient.TargetNamespace, withTarget("load-balancer", "api-external")))
	apiInternalUrl, err := url.Parse(infrastructure.Status.APIServerInternalURL)
	if err != nil {
		return nil, err
	}
	templates = append(templates, connectivitycheckcontroller.NewPodNetworkConnectivityCheckTemplate(apiInternalUrl.Host, operatorclient.TargetNamespace, withTarget("load-balancer", "api-internal")))
	return templates, err
}

func (c *connectivityCheckTemplateProvider) findNodeForInternalIP(internalIP string) (*v1.Node, error) {
	switch internalIP {
	case "localhost", "127.0.0.1", "::1":
		return &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "localhost"}}, nil
	}
	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		for _, nodeAddress := range node.Status.Addresses {
			if nodeAddress.Type != v1.NodeInternalIP {
				continue
			}
			if internalIP == nodeAddress.Address {
				return node, nil
			}
		}
	}
	return nil, fmt.Errorf("no node found with internal IP %s", internalIP)
}

type endpointInfo struct {
	hostName string
	port     string
	nodeName string
}

func withTarget(label, nodeName string) func(check *v1alpha1.PodNetworkConnectivityCheck) {
	return connectivitycheckcontroller.WithTarget(label + "-" + nodeName)
}
