package webhooksupportabilitycontroller

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/crypto"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestAssertService(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	err := indexer.Add(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &webhookSupportabilityController{
		serviceLister: corev1listers.NewServiceLister(indexer),
	}
	testCases := []struct {
		name      string
		service   *serviceReference
		expectErr bool
	}{
		{
			name:    "Happy",
			service: &serviceReference{Name: "test", Namespace: "test"},
		},
		{
			name:      "WrongName",
			service:   &serviceReference{Name: "wrong", Namespace: "test"},
			expectErr: true,
		},
		{
			name:      "WrongNamespace",
			service:   &serviceReference{Name: "test", Namespace: "wrong"},
			expectErr: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.assertService(tc.service)
			if tc.expectErr && err == nil {
				t.Fatalf("error expected")
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("error not expected: %s", err)
			}
		})
	}
}

func requireCondition(t *testing.T, expected, actual operatorv1.OperatorCondition) {
	matched, err := regexp.MatchString(`^`+expected.Message+`$`, actual.Message)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("condition message did not match:\nExpected:\n" + expected.Message + "\nActual:\n" + actual.Message)
	}
	expected.Message = ""
	actual.Message = ""
	actual.LastTransitionTime = metav1.Time{}
	if !cmp.Equal(actual, expected) {
		t.Fatal(cmp.Diff(expected, actual))
	}
}

func webhookServer(c, ns, n string, options ...func(*mockWebhookServer)) *mockWebhookServer {
	s := &mockWebhookServer{
		Config:   c,
		Service:  serviceReference{Namespace: ns, Name: n},
		Hostname: n + "." + ns + ".svc",
	}
	for _, o := range options {
		o(s)
	}
	return s
}

func doNotStart() func(*mockWebhookServer) {
	return func(s *mockWebhookServer) {
		s.doNotStart = true
	}
}

func withWrongCABundle(t *testing.T) func(*mockWebhookServer) {
	return func(s *mockWebhookServer) {
		cfg, err := crypto.MakeSelfSignedCAConfig(t.Name()+"WrongCA", 10*24*time.Hour)
		if err == nil {
			s.CABundle, _, err = cfg.GetPEMBytes()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func withEmptyCABundle(s *mockWebhookServer) {
	s.skipCABundleInjection = true
}

type mockWebhookServer struct {
	Config                string
	Service               serviceReference
	Hostname              string
	Port                  *int32
	CABundle              []byte
	skipCABundleInjection bool
	doNotStart            bool
}

// Run starts the mock server. Port and CABundle are available after this method returns.
func (s *mockWebhookServer) Run(t *testing.T, ctx context.Context) {
	// CA certs
	rootCACertCfg, err := crypto.MakeSelfSignedCAConfig(t.Name()+"RootCA", 10*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rootCA := &crypto.CA{SerialGenerator: &crypto.RandomSerialGenerator{}, Config: rootCACertCfg}
	if len(s.CABundle) == 0 {
		s.CABundle, _, err = rootCA.Config.GetPEMBytes()
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.skipCABundleInjection {
		s.CABundle = []byte{}
	}
	// server certs
	serverCertCfg, err := rootCA.MakeServerCert(sets.New(s.Hostname, "127.0.0.1"), 10*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := serverCertCfg.GetPEMBytes()
	if err != nil {
		t.Fatal(err)
	}
	serverKeyPair, err := tls.X509KeyPair(public, private)
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// get listen port
	_, str, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	i64, err := strconv.ParseInt(str, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	port := int32(i64)
	s.Port = &port

	// shutdown listener if test calls for it
	if s.doNotStart {
		listener.Close()
		return
	}

	// create and start server
	server := &http.Server{TLSConfig: &tls.Config{Certificates: []tls.Certificate{serverKeyPair}}}
	go func() {
		if err := server.ServeTLS(listener, "", ""); err != http.ErrServerClosed {
			t.Log(err)
		}
	}()

	// close server on context cancelled
	go func() {
		<-ctx.Done()
		server.Close()
	}()
}
