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
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	admissionregistrationv1listers "k8s.io/client-go/listers/admissionregistration/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestUpdateMutatingAdmissionWebhookConfigurationDegraded(t *testing.T) {

	testCases := []struct {
		name           string
		webhookConfigs []*admissionregistrationv1.MutatingWebhookConfiguration
		services       []*corev1.Service
		webhookServers []*mockWebhookServer
		expected       operatorv1.OperatorCondition
	}{
		{
			name: "None",
			expected: operatorv1.OperatorCondition{
				Type:   MutatingAdmissionWebhookConfigurationDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "HappyPath",
			webhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingServiceReference("ns10", "svc10")),
				),
				mutatingWebhookConfiguration("mwc20",
					withMutatingWebhook("mw20", withMutatingServiceReference("ns20", "svc20")),
					withMutatingWebhook("mw21", withMutatingServiceReference("ns21", "svc21")),
					withMutatingWebhook("mw22", withMutatingServiceReference("ns22", "svc22")),
				),
				mutatingWebhookConfiguration("mwc30",
					withMutatingWebhook("mw30", withMutatingServiceReference("ns30", "svc30")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
				service("ns20", "svc20"),
				service("ns21", "svc21"),
				service("ns22", "svc22"),
				service("ns30", "svc30"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc10", "ns10", "svc10"),
				webhookServer("mwc20", "ns20", "svc20"),
				webhookServer("mwc20", "ns21", "svc21"),
				webhookServer("mwc20", "ns22", "svc22"),
				webhookServer("mwc30", "ns30", "svc30"),
			},
			expected: operatorv1.OperatorCondition{
				Type:   MutatingAdmissionWebhookConfigurationDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "Ignore",
			webhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10",
						withMutatingServiceReference("ns10", "svc10"),
						withMutatingFailurePolicy(admissionregistrationv1.Ignore),
					),
				),
				mutatingWebhookConfiguration("mwc20",
					withMutatingWebhook("mw20",
						withMutatingServiceReference("ns20", "svc20"),
						withMutatingFailurePolicy(admissionregistrationv1.Ignore),
					),
				),
			},
			services: []*corev1.Service{
				service("ns20", "svc20"),
			},
			webhookServers: []*mockWebhookServer{},
			expected: operatorv1.OperatorCondition{
				Type:   MutatingAdmissionWebhookConfigurationDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "MultipleProblems",
			webhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingServiceReference("ns10", "svc10")),
				),
				mutatingWebhookConfiguration("mwc20",
					withMutatingWebhook("mw20", withMutatingServiceReference("ns20", "svc20")),
				),
				mutatingWebhookConfiguration("mwc30",
					withMutatingWebhook("mw30", withMutatingServiceReference("ns30", "svc30")),
				),
			},
			services: []*corev1.Service{
				service("ns20", "svc20"),
				service("ns30", "svc30"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc30", "ns30", "svc30"),
			},
			expected: operatorv1.OperatorCondition{
				Type:    MutatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceNotReadyReason,
				Message: `mw10: unable to find find service svc10.ns10: service "svc10" not found\nmw20: (?:.*)?dial tcp: lookup svc20.ns20.svc on .+: no such host`,
			},
		},
		{
			name: "DNSLookupFailure",
			webhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingServiceReference("ns10", "svc10")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: nil,
			expected: operatorv1.OperatorCondition{
				Type:    MutatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `mw10: (?:.*)?dial tcp: lookup svc10.ns10.svc on .+: no such host`,
			},
		},
		{
			name: "NotReachable",
			webhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingServiceReference("ns10", "svc10")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc10", "ns10", "svc10", doNotStart()),
			},
			expected: operatorv1.OperatorCondition{
				Type:    MutatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `mw10: (?:.*)?dial tcp 127.0.0.1:[0-9]+: connect: connection refused`,
			},
		},
		{
			name: "BadCABundle",
			webhookConfigs: []*admissionregistrationv1.MutatingWebhookConfiguration{
				mutatingWebhookConfiguration("mwc10",
					withMutatingWebhook("mw10", withMutatingServiceReference("ns10", "svc10")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc10", "ns10", "svc10", withWrongCABundle(t)),
			},
			expected: operatorv1.OperatorCondition{
				Type:    MutatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `mw10: (?:.*)?x509: certificate signed by unknown authority`,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := webhookSupportabilityController{}

			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, o := range tc.webhookConfigs {
				if err := indexer.Add(o); err != nil {
					t.Fatal(err)
				}
			}
			c.mutatingWebhookLister = admissionregistrationv1listers.NewMutatingWebhookConfigurationLister(indexer)

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
				cfg, err := c.mutatingWebhookLister.Get(server.Config)
				if err != nil {
					t.Fatal(err)
				}
				for i, webhook := range cfg.Webhooks {
					reference := webhook.ClientConfig.Service
					if reference.Namespace == server.Service.Namespace &&
						reference.Name == server.Service.Name {
						server.Run(t, ctx)
						// after starting, get port and CABundle
						reference.Port = server.Port
						cfg.Webhooks[i].ClientConfig.CABundle = server.CABundle
					}
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

			result := c.updateMutatingAdmissionWebhookConfigurationDegraded(ctx)
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

func TestUpdateValidatingAdmissionWebhookConfigurationDegradedStatus(t *testing.T) {

	testCases := []struct {
		name           string
		webhookConfigs []*admissionregistrationv1.ValidatingWebhookConfiguration
		services       []*corev1.Service
		webhookServers []*mockWebhookServer
		expected       operatorv1.OperatorCondition
	}{
		{
			name: "None",
			expected: operatorv1.OperatorCondition{
				Type:   ValidatingAdmissionWebhookConfigurationDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "HappyPath",
			webhookConfigs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				validatingWebhookConfiguration("mwc10",
					withValidatingWebhook("mw10", withValidatingServiceReference("ns10", "svc10")),
				),
				validatingWebhookConfiguration("mwc20",
					withValidatingWebhook("mw20", withValidatingServiceReference("ns20", "svc20")),
					withValidatingWebhook("mw21", withValidatingServiceReference("ns21", "svc21")),
					withValidatingWebhook("mw22", withValidatingServiceReference("ns22", "svc22")),
				),
				validatingWebhookConfiguration("mwc30",
					withValidatingWebhook("mw30", withValidatingServiceReference("ns30", "svc30")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
				service("ns20", "svc20"),
				service("ns21", "svc21"),
				service("ns22", "svc22"),
				service("ns30", "svc30"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc10", "ns10", "svc10"),
				webhookServer("mwc20", "ns20", "svc20"),
				webhookServer("mwc20", "ns21", "svc21"),
				webhookServer("mwc20", "ns22", "svc22"),
				webhookServer("mwc30", "ns30", "svc30"),
			},
			expected: operatorv1.OperatorCondition{
				Type:   ValidatingAdmissionWebhookConfigurationDegradedType,
				Status: operatorv1.ConditionFalse,
			},
		},
		{
			name: "MultipleProblems",
			webhookConfigs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				validatingWebhookConfiguration("mwc10",
					withValidatingWebhook("mw10", withValidatingServiceReference("ns10", "svc10")),
				),
				validatingWebhookConfiguration("mwc20",
					withValidatingWebhook("mw20", withValidatingServiceReference("ns20", "svc20")),
				),
				validatingWebhookConfiguration("mwc30",
					withValidatingWebhook("mw30", withValidatingServiceReference("ns30", "svc30")),
				),
			},
			services: []*corev1.Service{
				service("ns20", "svc20"),
				service("ns30", "svc30"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc30", "ns30", "svc30"),
			},
			expected: operatorv1.OperatorCondition{
				Type:    ValidatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceNotReadyReason,
				Message: `mw10: unable to find find service svc10.ns10: service \"svc10\" not found\nmw20: (?:.*)?dial tcp: lookup svc20.ns20.svc on .+: no such host`,
			},
		},
		{
			name: "DNSLookupFailure",
			webhookConfigs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				validatingWebhookConfiguration("mwc10",
					withValidatingWebhook("mw10", withValidatingServiceReference("ns10", "svc10")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: nil,
			expected: operatorv1.OperatorCondition{
				Type:    ValidatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `mw10: (?:.*)?dial tcp: lookup svc10.ns10.svc on .+: no such host`,
			},
		},
		{
			name: "NotReachable",
			webhookConfigs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				validatingWebhookConfiguration("mwc10",
					withValidatingWebhook("mw10", withValidatingServiceReference("ns10", "svc10")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc10", "ns10", "svc10", doNotStart()),
			},
			expected: operatorv1.OperatorCondition{
				Type:    ValidatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `mw10: (?:.*)?dial tcp 127.0.0.1:[0-9]+: connect: connection refused`,
			},
		},
		{
			name: "BadCABundle",
			webhookConfigs: []*admissionregistrationv1.ValidatingWebhookConfiguration{
				validatingWebhookConfiguration("mwc10",
					withValidatingWebhook("mw10", withValidatingServiceReference("ns10", "svc10")),
				),
			},
			services: []*corev1.Service{
				service("ns10", "svc10"),
			},
			webhookServers: []*mockWebhookServer{
				webhookServer("mwc10", "ns10", "svc10", withWrongCABundle(t)),
			},
			expected: operatorv1.OperatorCondition{
				Type:    ValidatingAdmissionWebhookConfigurationDegradedType,
				Status:  operatorv1.ConditionTrue,
				Reason:  WebhookServiceConnectionErrorReason,
				Message: `mw10: (?:.*)?x509: certificate signed by unknown authority`,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := webhookSupportabilityController{}

			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			for _, o := range tc.webhookConfigs {
				if err := indexer.Add(o); err != nil {
					t.Fatal(err)
				}
			}
			c.validatingWebhookLister = admissionregistrationv1listers.NewValidatingWebhookConfigurationLister(indexer)

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
				cfg, err := c.validatingWebhookLister.Get(server.Config)
				if err != nil {
					t.Fatal(err)
				}
				for i, webhook := range cfg.Webhooks {
					reference := webhook.ClientConfig.Service
					if reference.Namespace == server.Service.Namespace &&
						reference.Name == server.Service.Name {
						server.Run(t, ctx)
						// after starting, get port and CABundle
						reference.Port = server.Port
						cfg.Webhooks[i].ClientConfig.CABundle = server.CABundle
					}
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

			result := c.updateValidatingAdmissionWebhookConfigurationDegradedStatus(ctx)
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

func mutatingWebhookConfiguration(n string, options ...func(*admissionregistrationv1.MutatingWebhookConfiguration)) *admissionregistrationv1.MutatingWebhookConfiguration {
	c := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: n},
	}
	for _, o := range options {
		o(c)
	}
	return c
}

func withMutatingWebhook(n string, options ...func(*admissionregistrationv1.MutatingWebhook)) func(*admissionregistrationv1.MutatingWebhookConfiguration) {
	return func(c *admissionregistrationv1.MutatingWebhookConfiguration) {
		w := &admissionregistrationv1.MutatingWebhook{
			Name: n,
		}
		for _, o := range options {
			o(w)
		}
		c.Webhooks = append(c.Webhooks, *w)
	}
}

func withMutatingServiceReference(ns, n string) func(*admissionregistrationv1.MutatingWebhook) {
	return func(w *admissionregistrationv1.MutatingWebhook) {
		w.ClientConfig = admissionregistrationv1.WebhookClientConfig{
			Service: &admissionregistrationv1.ServiceReference{
				Namespace: ns,
				Name:      n,
			},
		}
	}
}

func withMutatingFailurePolicy(p admissionregistrationv1.FailurePolicyType) func(*admissionregistrationv1.MutatingWebhook) {
	return func(w *admissionregistrationv1.MutatingWebhook) {
		w.FailurePolicy = &p
	}
}

func validatingWebhookConfiguration(n string, options ...func(*admissionregistrationv1.ValidatingWebhookConfiguration)) *admissionregistrationv1.ValidatingWebhookConfiguration {
	c := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: n},
	}
	for _, o := range options {
		o(c)
	}
	return c
}

func withValidatingWebhook(n string, options ...func(*admissionregistrationv1.ValidatingWebhook)) func(*admissionregistrationv1.ValidatingWebhookConfiguration) {
	return func(c *admissionregistrationv1.ValidatingWebhookConfiguration) {
		w := &admissionregistrationv1.ValidatingWebhook{
			Name: n,
		}
		for _, o := range options {
			o(w)
		}
		c.Webhooks = append(c.Webhooks, *w)
	}
}

func withValidatingServiceReference(ns, n string) func(*admissionregistrationv1.ValidatingWebhook) {
	return func(w *admissionregistrationv1.ValidatingWebhook) {
		w.ClientConfig = admissionregistrationv1.WebhookClientConfig{
			Service: &admissionregistrationv1.ServiceReference{
				Namespace: ns,
				Name:      n,
			},
		}
	}
}

func service(ns, n string) *corev1.Service {
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n},
	}
	return s
}
