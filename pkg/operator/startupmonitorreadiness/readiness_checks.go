package startupmonitorreadiness

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/staticpod/startupmonitor"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
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

	kubeClient *kubernetes.Clientset

	// currentNodeName holds the name of the node we are currently running on
	// primarly introduced for easier testing on an HA cluster
	currentNodeName string
}

var _ startupmonitor.ReadinessChecker = &KubeAPIReadinessChecker{}
var _ startupmonitor.WantsRestConfig = &KubeAPIReadinessChecker{}
var _ startupmonitor.WantsNodeName = &KubeAPIReadinessChecker{}

// New creates a new Kube API readiness checker
func New() *KubeAPIReadinessChecker {
	return &KubeAPIReadinessChecker{
		baseRawURL: "https://localhost:6443",
	}
}

// SetRestConfig called by startup monitor to provide a valid configuration for authN/authZ against Kube API server
func (ch *KubeAPIReadinessChecker) SetRestConfig(config *rest.Config) {
	ch.restConfig = config

	// note that we will be talking to Kube API over localhost and in case of an error/timeout requests will be retired for 5 min.
	// setting the global timeout to a short value seems to be fine
	ch.restConfig.Timeout = 4 * time.Second

	ch.restConfig.Burst = 15
	ch.restConfig.QPS = 10
}

// SetNodeName called by startup monitor to provide the current node name
func (ch *KubeAPIReadinessChecker) SetNodeName(nodeName string) {
	ch.currentNodeName = nodeName
}

// IsReady performs a series of checks for assessing Kube API server readiness condition
func (ch *KubeAPIReadinessChecker) IsReady(ctx context.Context, revision int) ( /*ready*/ bool /*reason*/, string /*message*/, string /*err*/, error) {
	if ch.restConfig == nil {
		return false, "", "", fmt.Errorf("missing restConfig, use SetRestConfig() metod to set one")

	}
	if ch.client == nil {
		client, err := createHTTPClient(ch.restConfig)
		if err != nil {
			return false, "", "", fmt.Errorf("failed to create an HTTP client due to %v", err)
		}
		ch.client = client
	}

	if ch.kubeClient == nil {
		kubeClient, err := kubernetes.NewForConfig(ch.restConfig)
		if err != nil {
			return false, "", "", fmt.Errorf("failed to create kubernetes clientset due to %v", err)
		}
		ch.kubeClient = kubeClient
	}

	if len(ch.currentNodeName) == 0 {
		return false, "", "", fmt.Errorf("a node name is required, use the SetNodeName method")
	}

	// loop through a list of ordered checks for assessing Kube API readiness condition
	for _, checkFn := range []func(context.Context) (bool, string, string){
		//	TODO: watch /var/log/kube-apiserver/termination.log for the first start-up attempt (beware of the race of startup-monitor startup and kube-apiserver startup). Set Reason=NeverStartedUp when this times out.
		//	TODO: watch /var/log/kube-apiserver/termination.log for more than one start-up attempt. Set Reason=CrashLooping if more than one is found and the monitor times out.

		// checks if we are not dealing with the old kas
		noOldRevisionPodExists(ch.kubeClient.CoreV1().Pods(operatorclient.TargetNamespace), revision, ch.currentNodeName),

		// check kube-apiserver /healthz/etcd endpoint
		goodHealthzEtcdEndpoint(ch.client, ch.baseRawURL),

		// check kube-apiserver /healthz endpoint
		goodHealthzEndpoint(ch.client, ch.baseRawURL),

		// check kube-apiserver /readyz endpoint
		goodReadyzEndpoint(ch.client, ch.baseRawURL, 3, 5*time.Second),

		// check if the kas pod is running at the expected revision
		newRevisionPodExists(ch.kubeClient.CoreV1().Pods(operatorclient.TargetNamespace), revision, ch.currentNodeName),

		// check that kubelet has reporting readiness for the new pod
		newPodRunning(ch.kubeClient.CoreV1().Pods(operatorclient.TargetNamespace), revision, ch.currentNodeName),
	} {
		select {
		case <-ctx.Done():
			return false, "", "", ctx.Err()
		default:
		}

		if ready, reason, message := checkFn(ctx); !ready {
			return ready, reason, message, nil
		}
	}

	// at this point Kube API is ready!
	return true, "", "", nil
}

// newPodRunning checks if kas pod is in PodRunning phase and has PodReady condition set to true
func newPodRunning(podClient corev1client.PodInterface, monitorRevision int, currentNodeName string) func(context.Context) (bool, string, string) {
	return func(ctx context.Context) (bool, string, string) {
		apiServerPods, err := podClient.List(ctx, metav1.ListOptions{LabelSelector: "apiserver=true"})
		if err != nil {
			return false, "PodListError", fmt.Sprintf("failed to list kube-apiserver static pods: %v", err)
		}

		var kasPod corev1.Pod
		filteredKasPods := filterByNodeName(apiServerPods.Items, currentNodeName)
		switch len(filteredKasPods) {
		case 0:
			return false, "PodNotRunning", fmt.Sprintf("waiting for kube-apiserver static pod for node %s to show up", currentNodeName)
		case 1:
			kasPod = filteredKasPods[0]
		default:
			// this should never happen for static pod as they are uniquely named for each node
			podsOnCurrentNode := []string{}
			for _, filteredKasPod := range filteredKasPods {
				podsOnCurrentNode = append(podsOnCurrentNode, filteredKasPod.Name)
			}
			return false, "PodListError", fmt.Sprintf("multiple kube-apiserver static pods for node %s found: %v", currentNodeName, podsOnCurrentNode)
		}

		if kasPod.Status.Phase != corev1.PodRunning {
			return false, "PodNodReady", fmt.Sprintf("waiting for kube-apiserver static pod %s to be running: %s", kasPod.Name, kasPod.Status.Phase)
		}

		if kasPod.Status.Phase == corev1.PodRunning && !func(pod corev1.Pod) bool {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					return true
				}
			}
			return false
		}(kasPod) {
			return false, "PodNodReady", fmt.Sprintf("waiting for kube-apiserver static pod %s to be ready", kasPod.Name)
		}

		return checkRevision(&kasPod, monitorRevision)
	}
}

// newRevisionPodExists check if the kas pod is running at the expected revision
func newRevisionPodExists(podClient corev1client.PodInterface, monitorRevision int, currentNodeName string) func(context.Context) (bool, string, string) {
	return func(ctx context.Context) (bool, string, string) {
		return checkRevisionOnPod(ctx, podClient, monitorRevision, true, currentNodeName)
	}
}

// noOldRevisionPodExists checks if we are not dealing with the old kas
// it is useful when you want to avoid false positive - failing readyz check when the previous instance is still running
//
// note that:
// it won't fail when getting the pod from the api server fails as that might mean the new instance is not ready/healthy
func noOldRevisionPodExists(podClient corev1client.PodInterface, monitorRevision int, currentNodeName string) func(context.Context) (bool, string, string) {
	return func(ctx context.Context) (bool, string, string) {
		return checkRevisionOnPod(ctx, podClient, monitorRevision, false, currentNodeName)
	}
}

// checkRevisionOnPod checks if the kas pod is running at the expected revision
//
// strictMode controls whether a certain errors like: failing to get the pod or absence of the pod should be fatal
// it is useful when you want to avoid false positive - failing readyz check when the previous instance is still running
func checkRevisionOnPod(ctx context.Context, podClient corev1client.PodInterface, monitorRevision int, strictMode bool, currentNodeName string) (bool, string, string) {
	apiServerPods, err := podClient.List(ctx, metav1.ListOptions{LabelSelector: "apiserver=true"})
	if err != nil {
		return !strictMode, "PodListError", fmt.Sprintf("failed to list kube-apiserver static pods: %v", err)
	}

	var kasPod corev1.Pod
	filteredKasPods := filterByNodeName(apiServerPods.Items, currentNodeName)
	switch len(filteredKasPods) {
	case 0:
		return false, "PodNotRunning", fmt.Sprintf("waiting for kube-apiserver static pod for node %s to show up", currentNodeName)
	case 1:
		kasPod = filteredKasPods[0]
	default:
		// this should never happen for static pod as they are uniquely named for each node
		podsOnCurrentNode := []string{}
		for _, filteredKasPod := range filteredKasPods {
			podsOnCurrentNode = append(podsOnCurrentNode, filteredKasPod.Name)
		}
		return false, "PodListError", fmt.Sprintf("multiple kube-apiserver static pods for node %s found: %v", currentNodeName, podsOnCurrentNode)
	}

	return checkRevision(&kasPod, monitorRevision)
}

func checkRevision(kasPod *corev1.Pod, monitorRevision int) (bool, string, string) {
	revisionString, found := kasPod.Labels["revision"]
	if !found {
		return false, "InvalidPod", fmt.Sprintf("missing revision label on static pod %s", kasPod.Name)
	}
	if len(revisionString) == 0 {
		return false, "InvalidPod", fmt.Sprintf("unexpected empty revision label on static pod %s", kasPod.Name)
	}
	revision, err := strconv.Atoi(revisionString)
	if err != nil || revision < 0 {
		return false, "InvalidPod", fmt.Sprintf("invalid revision label on static pod %s: %q", kasPod.Name, revisionString)
	}

	if revision != monitorRevision {
		return false, "UnexpectedRevision", fmt.Sprintf("waiting for kube-apiserver static pod %s of revision %d, found %d", kasPod.Name, monitorRevision, revision)
	}

	return true, "", ""
}

// goodReadyzEndpoint performs HTTP checks against readyz?verbose=true endpoint
//  returns true, "", "", when we got HTTP 200 "successThreshold" times
//  returns false, "NotReady", EntireResponseBody (if any) on HTTP != 200
//  returns false, "NotReadyError", EntireResponseBody (if any) in case of any error or timeout
func goodReadyzEndpoint(client *http.Client, rawURL string, successThreshold int, interval time.Duration) func(ctx context.Context) (bool, string, string) {
	return func(ctx context.Context) (bool, string, string) {
		return doHTTPCheckAndTransform(ctx, client, fmt.Sprintf("%s/readyz?verbose=true", rawURL), "NotReady", doHTTPCheckMultipleTimes(successThreshold, interval))
	}
}

// goodHealthzEndpoint performs an HTTP check against healthz?verbose=true endpoint
//  returns true, "", "", on HTTP 200
//  returns false, "Unhealthy", EntireResponseBody (if any) on HTTP != 200
//  returns false, "UnhealthyError", EntireResponseBody (if any) in case of any error or timeout
func goodHealthzEndpoint(client *http.Client, rawURL string) func(context.Context) (bool, string, string) {
	return func(ctx context.Context) (bool, string, string) {
		return doHTTPCheckAndTransform(ctx, client, fmt.Sprintf("%s/healthz?verbose=true", rawURL), "Unhealthy", doHTTPCheck)
	}
}

// goodHealthzEtcdEndpoint performs an HTTP check against healthz/etcd endpoint
//  returns true, "", "", on HTTP 200
//  returns false, "EtcdUnhealthy", EntireResponseBody (if any) on HTTP != 200
//  returns false, "EtcdUnhealthyError", EntireResponseBody (if any) in case of any error or timeout
func goodHealthzEtcdEndpoint(client *http.Client, rawURL string) func(context.Context) (bool, string, string) {
	return func(ctx context.Context) (bool, string, string) {
		return doHTTPCheckAndTransform(ctx, client, fmt.Sprintf("%s/healthz/etcd", rawURL), "EtcdUnhealthy", doHTTPCheck)
	}
}

func doHTTPCheckAndTransform(ctx context.Context, client *http.Client, rawURL string, checkName string, httpCheckFn func(ctx context.Context, client *http.Client, rawURL string) (int, string, error)) (bool, string, string) {
	statusCode, response, err := httpCheckFn(ctx, client, rawURL)
	if err != nil {
		if utilnet.IsConnectionRefused(err) {
			return false, "NetworkError", fmt.Sprintf("waiting for kube-apiserver static pod to listen on port 6443: %v", err)
		}
		if utilnet.IsConnectionReset(err) || utilnet.IsProbableEOF(err) {
			return false, "NetworkError", fmt.Sprintf("failed sending request to kube-apiserver: %v", err)
		}
		if utilnet.IsTimeout(err) {
			return false, "NetworkError", fmt.Sprintf("request to kube-apiserver static pod timed out: %v", err)
		}
		errMsg := err.Error()
		if len(response) > 0 {
			errMsg = fmt.Sprintf("%v, a response from the server was %v", errMsg, response)
		}
		return false, "UnknownError", errMsg
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

func createHTTPClient(restConfig *rest.Config) (*http.Client, error) {
	transportConfig, err := restConfig.TransportConfig()
	if err != nil {
		return nil, err
	}

	rt, err := transport.New(transportConfig)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: rt,
		Timeout:   restConfig.Timeout,
	}

	return client, nil
}

// doHTTPCheckMultipleTimes calls doHTTPCheck "n" times with an "interval" between each invocation
// it stops on a non 200 HTTP status code or when an error is returned from doHTTPCheck method
func doHTTPCheckMultipleTimes(n int, interval time.Duration) func(ctx context.Context, client *http.Client, rawURL string) (int, string, error) {
	return func(ctx context.Context, client *http.Client, rawURL string) (int, string, error) {
		var lastResponse string
		var lastError error
		var lastStatusCode int
		for i := 1; i <= n; i++ {
			lastStatusCode, lastResponse, lastError = doHTTPCheck(ctx, client, rawURL)
			if lastError != nil || lastStatusCode != http.StatusOK {
				return lastStatusCode, lastResponse, lastError
			}
			if i != n {
				time.Sleep(interval)
			}
		}
		return lastStatusCode, lastResponse, lastError
	}
}

func filterByNodeName(kasPods []corev1.Pod, currentNodeName string) []corev1.Pod {
	filteredKasPods := []corev1.Pod{}

	for _, potentialKasPods := range kasPods {
		if potentialKasPods.Spec.NodeName == currentNodeName {
			filteredKasPods = append(filteredKasPods, potentialKasPods)
		}
	}

	return filteredKasPods
}
