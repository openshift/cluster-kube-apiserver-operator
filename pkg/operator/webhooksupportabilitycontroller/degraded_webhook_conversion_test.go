package webhooksupportabilitycontroller

import (
	"context"
	"io"
	"log"
	"net"
	"testing"

	"github.com/foxcpp/go-mockdns"
	"github.com/miekg/dns"
	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1listers "k8s.io/apiextensions-apiserver/pkg/client/listers/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestUpdateCRDConversionWebhookConfigurationDegraded(t *testing.T) {

	testCases := []struct {
		name           string
		crds           []*apiextensionsv1.CustomResourceDefinition
		services       []*corev1.Service
		webhookServers []*mockWebhookServer
		expected       operatorv1.OperatorCondition
	}{
		{
			name: "None",
			expected: operatorv1.OperatorCondition{
				Type:   CRDConversionWebhookConfigurationDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "HappyPath",
			crds: []*apiextensionsv1.CustomResourceDefinition{
				customResourceDefinition("crd10",
					withConversionServiceReference("ns10", "svc10"),
				),
				customResourceDefinition("crd20",
					withConversionServiceReference("ns20", "svc20"),
				),
				customResourceDefinition("crd30",
					withConversionServiceReference("ns30", "svc30"),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
				service("ns20", "svc20"),
				service("ns30", "svc30"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("crd10", "ns10", "svc10"),
				webhookServer("crd20", "ns20", "svc20"),
				webhookServer("crd30", "ns30", "svc30"),
			},
			expected: operatorv1.OperatorCondition{
				Type:   CRDConversionWebhookConfigurationDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "MultipleProblems",
			crds: []*apiextensionsv1.CustomResourceDefinition{
				customResourceDefinition("crd10",
					withConversionServiceReference("ns10", "svc10"),
				),
				customResourceDefinition("crd20",
					withConversionServiceReference("ns20", "svc20"),
				),
				customResourceDefinition("crd30",
					withConversionServiceReference("ns30", "svc30"),
				),
			},
			services: []*corev1.Service{
				service("ns20", "svc20"),
				service("ns30", "svc30"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("crd30", "ns30", "svc30"),
			},
			expected: operatorv1.OperatorCondition{
				Type:    CRDConversionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceNotReadyReason,
				Message: `crd10: unable to find find service svc10.ns10: service "svc10" not found\ncrd20: (?:.*)?dial tcp: lookup svc20.ns20.svc on .+: no such host`,
			},
		},
		{
			name: "DNSLookupFailure",
			crds: []*apiextensionsv1.CustomResourceDefinition{
				customResourceDefinition("crd10",
					withConversionServiceReference("ns10", "svc10"),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: nil,
			expected: operatorv1.OperatorCondition{
				Type:    CRDConversionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `crd10: (?:.*)?dial tcp: lookup svc10.ns10.svc on .+: no such host`,
			},
		},
		{
			name: "NotReachable",
			crds: []*apiextensionsv1.CustomResourceDefinition{
				customResourceDefinition("crd10",
					withConversionServiceReference("ns10", "svc10"),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("crd10", "ns10", "svc10", doNotStart()),
			},
			expected: operatorv1.OperatorCondition{
				Type:    CRDConversionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `crd10: (?:.*)?dial tcp 127.0.0.1:[0-9]+: connect: connection refused`,
			},
		},
		{
			name: "BadCABundle",
			crds: []*apiextensionsv1.CustomResourceDefinition{
				customResourceDefinition("crd10",
					withConversionServiceReference("ns10", "svc10"),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("crd10", "ns10", "svc10", withWrongCABundle(t)),
			},
			expected: operatorv1.OperatorCondition{
				Type:    CRDConversionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `crd10: (?:.*)?x509: certificate signed by unknown authority`,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := webhookSupportabilityController{}

			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, o := range tc.crds {
				if err := indexer.Add(o); err != nil {
					t.Fatal(err)
				}
			}
			c.crdLister = apiextensionsv1listers.NewCustomResourceDefinitionLister(indexer)

			indexer = cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, o := range tc.services {
				if err := indexer.Add(o); err != nil {
					t.Fatal(err)
				}
			}
			c.serviceLister = corev1listers.NewServiceLister(indexer)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// start the mock webhook servers
			for _, server := range tc.webhookServers {
				// get the corresponding config, so we can update the port and CABundle
				crd, err := c.crdLister.Get(server.Config)
				if err != nil {
					t.Fatal(err)
				}
				reference := crd.Spec.Conversion.Webhook.ClientConfig.Service
				if reference.Namespace == server.Service.Namespace &&
					reference.Name == server.Service.Name {
					server.Run(t, ctx)
					// after starting, get port and CABundle
					reference.Port = server.Port
					crd.Spec.Conversion.Webhook.ClientConfig.CABundle = server.CABundle
				}
			}

			// start a dns server to resolve the webhook server services
			zones := map[string]mockdns.Zone{}
			for _, server := range tc.webhookServers {
				zones[dns.Fqdn(server.Hostname)] = mockdns.Zone{A: []string{"127.0.0.1"}}
			}
			dnsServer, err := mockdns.NewServerWithLogger(zones, log.New(io.Discard, "", log.LstdFlags), false)
			if err != nil {
				t.Fatal(err)
			}
			defer dnsServer.Close()
			dnsServer.PatchNet(net.DefaultResolver)
			defer mockdns.UnpatchNet(net.DefaultResolver)

			result := c.updateCRDConversionWebhookConfigurationDegraded(ctx)
			status := &operatorv1.OperatorStatus{}
			err = result(status)
			if err != nil {
				t.Fatal(err)
			}
			if len(status.Conditions) != 1 {
				t.Log(status)
				t.Fatal("expected exactly one condition")
			}
			requireCondition(t, tc.expected, status.Conditions[0])
		})
	}
}

func customResourceDefinition(n string, options ...func(*apiextensionsv1.CustomResourceDefinition)) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: n},
	}
	for _, o := range options {
		o(crd)
	}
	return crd
}

func withConversionServiceReference(ns, n string) func(*apiextensionsv1.CustomResourceDefinition) {
	return func(crd *apiextensionsv1.CustomResourceDefinition) {
		c := &apiextensionsv1.CustomResourceConversion{
			Strategy: apiextensionsv1.WebhookConverter,
			Webhook: &apiextensionsv1.WebhookConversion{
				ClientConfig: &apiextensionsv1.WebhookClientConfig{
					Service: &apiextensionsv1.ServiceReference{
						Namespace: ns,
						Name:      n,
					},
				},
			},
		}
		crd.Spec.Conversion = c
	}
}
