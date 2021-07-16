package startupmonitorreadiness

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

// KubeAPIReadinessChecker is a struct that holds necessary data
// to perform a set of checks against a Kube API server to assess its health condition
type KubeAPIReadinessChecker struct {
	// configuration for authN/authZ against the server
	// populated from kubeconfig and set by the startup monitor pod
	restConfig *rest.Config

	// client we use to perform HTTP checks
	client *http.Client
	// defined here for easier testing
	baseRawURL string
}

// TODO: uncomment when https://github.com/openshift/library-go/pull/1130 merges
//var _ startupmonitor.ReadinessChecker = &KubeAPIReadinessChecker{}
// var _ startupmonitor.WantsRestConfig = &KubeAPIReadinessChecker{}

// New creates a new Kube API readiness checker
func New() *KubeAPIReadinessChecker {
	return &KubeAPIReadinessChecker{
		baseRawURL: "https://localhost:6443",
	}
}

// SetRestConfig called by startup monitor to provide a valid configuration for authN/authZ against Kube API server
func (ch *KubeAPIReadinessChecker) SetRestConfig(restConfig *rest.Config) {
	ch.restConfig = restConfig
}

// IsReady performs a series of checks for assessing Kube API server readiness condition
func (ch *KubeAPIReadinessChecker) IsReady(ctx context.Context) ( /*ready*/ bool /*reason*/, string /*message*/, string /*err*/, error) {
	if ch.restConfig == nil {
		return false, "", "", fmt.Errorf("missing restConfig, use SetRestConfig() metod to set one")

	}
	if ch.client == nil {
		client, err := createHTTPClient(2*time.Second, ch.restConfig)
		if err != nil {
			return false, "", "", fmt.Errorf("failed to create an HTTP client due to %v", err)
		}
		ch.client = client
	}

	/*
		TODO: watch /var/log/kube-apiserver/termination.log for the first start-up attempt (beware of the race of startup-monitor startup and kube-apiserver startup). Set Reason=NeverStartedUp when this times out.
		TODO: watch /var/log/kube-apiserver/termination.log for more than one start-up attempt. Set Reason=CrashLooping if more than one is found and the monitor times out.
	*/

	if etcdHealthy, etcdHealthyReason, etcdHealthyMsg := doETCDHealthCheck(ctx, ch.client, ch.baseRawURL); !etcdHealthy {
		return etcdHealthy, etcdHealthyReason, etcdHealthyMsg, nil
	}

	/*
		TODO: check https://localhost:6443/healthz. Set Reason=Unhealthy if this is red and the monitor times out.
		TODO: check https://localhost:6443/readyz. Set Reason=NotReady if this is red and the monitor times out. Reason=EtcdUnhealthy if the etcd post-start-hook never finished. In all case: message should contain the unfinished post-start-hooks.
		TODO: get the mirror pod from the localhost kube-apiserver
		TODO: check the revision annotation is the expected one. Set Reason=NotReady if this is red and the monitor times out.
		TODO: checking status.ready. Set Reason=NotReady if this is red and the monitor times out.
	*/

	return true, "", "", nil
}

// doETCDHealthCheck performs an HTTP check against healthz/etcd endpoint
//  returns true, "", "", on HTTP 200
//  returns false, "EtcdUnhealthy", EntireResponseBody (if any) on HTTP != 200
//  returns false, "EtcdUnhealthyError", EntireResponseBody (if any) in case of any error or timeout
func doETCDHealthCheck(ctx context.Context, client *http.Client, rawURL string) (bool, string, string) {
	return doHTTPCheckAndTransform(ctx, client, fmt.Sprintf("%s/healthz/etcd", rawURL), "EtcdUnhealthy")
}

func doHTTPCheckAndTransform(ctx context.Context, client *http.Client, rawURL string, checkName string) (bool, string, string) {
	statusCode, response, err := doHTTPCheck(ctx, client, rawURL)
	if err != nil {
		errMsg := fmt.Sprintf("falied while performing the check due to %v", err)
		if len(response) > 0 {
			errMsg = fmt.Sprintf("%v, a response from the server was %v", errMsg, response)
		}
		return false, fmt.Sprintf("%vError", checkName), errMsg
	}
	if statusCode != http.StatusOK {
		return false, checkName, response
	}

	return true, "", ""
}

func doHTTPCheck(ctx context.Context, client *http.Client, rawURL string) (int, string, error) {
	targetURL, err := url.Parse(rawURL)
	if err != nil {
		return 0, "", err
	}
	newReq, err := http.NewRequestWithContext(ctx, "GET", targetURL.String(), nil)
	if err != nil {
		return 0, "", err
	}

	resp, err := client.Do(newReq)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	// we expect small responses from the server
	// so it is okay to read the entire body
	rawResponse, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("error while reading body from %v, err %v", targetURL.String(), err)
	}

	return resp.StatusCode, string(rawResponse), nil
}

func createHTTPClient(responseTimeout time.Duration, restConfig *rest.Config) (*http.Client, error) {
	transportConfig, err := restConfig.TransportConfig()
	if err != nil {
		return nil, err
	}

	tlsConfig, err := transport.TLSConfigFor(transportConfig)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: utilnet.SetTransportDefaults(&http.Transport{
			TLSClientConfig: tlsConfig,
		}),
		Timeout: responseTimeout,
	}

	return client, nil
}
