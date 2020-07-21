package connectivitycheckcontroller

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/openshift/api/operatorcontrolplane/v1alpha1"
	operatorcontrolplaneclient "github.com/openshift/client-go/operatorcontrolplane/clientset/versioned"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

func NewKubeAPIServerConnectivityCheckController(
	kubeClient kubernetes.Interface,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	operatorcontrolplaneClient *operatorcontrolplaneclient.Clientset,
	recorder events.Recorder,
) ConnectivityCheckController {
	templateProvider := &connectivityCheckTemplateProvider{
		kubeClient:      kubeClient,
		operatorClient:  operatorClient,
		endpointsLister: kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Endpoints().Lister(),
		serviceLister:   kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Services().Lister(),
	}

	c := NewConnectivityCheckController(
		operatorClient,
		operatorcontrolplaneClient,
		[]factory.Informer{
			kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Endpoints().Informer(),
			kubeInformersForNamespaces.InformersFor("openshift-apiserver").Core().V1().Services().Informer(),
		},
		recorder).
		WithPodNetworkConnectivityCheckFn(templateProvider.getPodNetworkConnectivityChecks)

	return c
}

type connectivityCheckTemplateProvider struct {
	kubeClient      kubernetes.Interface
	operatorClient  v1helpers.OperatorClient
	endpointsLister corev1listers.EndpointsLister
	serviceLister   corev1listers.ServiceLister
}

func (c *connectivityCheckTemplateProvider) getPodNetworkConnectivityChecks(ctx context.Context, syncContext factory.SyncContext) ([]*v1alpha1.PodNetworkConnectivityCheck, error) {
	var templates []*v1alpha1.PodNetworkConnectivityCheck
	// each storage endpoint
	etcdEndpoints, err := c.getTemplatesForEtcdEndpoints(syncContext)
	if err != nil {
		return nil, fmt.Errorf("failed to list etcd IPs: %w", err)
	}
	templates = append(templates, etcdEndpoints...)

	// oas service IP
	oasServiceIP, err := c.getTemplatesForOpenShiftAPIServerService(syncContext)
	if err != nil {
		return nil, fmt.Errorf("failed to list openshift-apiserver service IP: %w", err)
	}
	templates = append(templates, oasServiceIP...)

	// each oas endpoint
	oasEndpointIPs, err := c.getTemplatesForOpenShiftAPIServerEndpoints(syncContext)
	if err != nil {
		return nil, fmt.Errorf("failed to list openshift-apiserver endpoint IPs: %w", err)
	}
	templates = append(templates, oasEndpointIPs...)

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
			check.Name = strings.Replace(check.Name, "$(SOURCE)", staticPodName, -1)
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
		templates = append(templates, NewPodNetworkProductivityCheckTemplate("openshift-apiserver-service", address))
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
	ips, err := c.listAddressesForOpenShiftAPIServerServiceEndpoints(syncContext)
	if err != nil {
		return nil, err
	}
	for _, address := range ips {
		templates = append(templates, NewPodNetworkProductivityCheckTemplate("openshift-apiserver-endpoint", address))
	}
	return templates, nil
}

// listAddressesForOpenShiftAPIServerServiceEndpoints returns oas api service endpoints ip
func (c *connectivityCheckTemplateProvider) listAddressesForOpenShiftAPIServerServiceEndpoints(syncContext factory.SyncContext) ([]string, error) {
	var results []string
	endpoints, err := c.endpointsLister.Endpoints("openshift-apiserver").Get("api")
	if err != nil {
		return nil, err
	}
	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			for _, port := range subset.Ports {
				results = append(results, net.JoinHostPort(address.IP, strconv.Itoa(int(port.Port))))
			}
		}
	}
	return results, nil
}

func (c *connectivityCheckTemplateProvider) getTemplatesForEtcdEndpoints(syncContext factory.SyncContext) ([]*v1alpha1.PodNetworkConnectivityCheck, error) {
	var templates []*v1alpha1.PodNetworkConnectivityCheck
	ips, err := c.listAddressesForEtcdServerEndpoints(syncContext)
	if err != nil {
		return nil, err
	}
	for _, address := range ips {
		templates = append(templates, NewPodNetworkProductivityCheckTemplate("etcd-server", address, WithTlsClientCert("etcd-client")))
	}
	return templates, nil
}

func (c *connectivityCheckTemplateProvider) listAddressesForEtcdServerEndpoints(syncContext factory.SyncContext) ([]string, error) {
	operatorSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return nil, fmt.Errorf("failed to get the operatorSpec: %w", err)
	}

	var results []string
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
		results = append(results, storageConfigURL.Host)
	}
	return results, nil
}
