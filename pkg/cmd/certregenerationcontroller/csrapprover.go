package certregenerationcontroller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"

	certapiv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	certv1client "k8s.io/client-go/kubernetes/typed/certificates/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	csrv1listers "k8s.io/client-go/listers/certificates/v1"
)

const (
	controllerName       = "control-plane-csr-approver"
	bootstrapperUsername = "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"
)

var (
	mcoUserGroups = sets.NewString(
		"system:authenticated",
		"system:serviceaccounts",
		"system:serviceaccounts:openshift-machine-config-operator",
	)
	kubeletUserGroups = sets.NewString("system:authenticated", "system:nodes")
)

type ControlPlaneCSRController interface {
	factory.Controller
}

type NodeAddresses struct {
	DNSNames    sets.String
	IPAddresses sets.String
}

type controller struct {
	factory.Controller
	readyControlPlaneNodesOnce map[string]NodeAddresses
	nodeGetter                 corev1client.NodesGetter
	csrLister                  csrv1listers.CertificateSigningRequestLister
	csrClient                  certv1client.CertificateSigningRequestInterface
}

func NewControlPlaneCSRController(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	kubeInformers informers.SharedInformerFactory,
	recorder events.Recorder,
) ControlPlaneCSRController {
	klog.Infof("Starting ControlPlaneCSRController")
	c := &controller{
		nodeGetter: kubeClient.CoreV1(),
		csrLister:  kubeInformers.Certificates().V1().CertificateSigningRequests().Lister(),
		csrClient:  kubeClient.CertificatesV1().CertificateSigningRequests(),
	}
	c.Controller = factory.New().
		WithSync(c.Sync).
		WithInformersQueueKeyFunc(
			func(obj runtime.Object) string {
				csr, ok := obj.(*certapiv1.CertificateSigningRequest)
				if !ok {
					return ""
				}
				return csr.Name
			},
			kubeInformers.Core().V1().Nodes().Informer(),
			kubeInformers.Certificates().V1().CertificateSigningRequests().Informer(),
		).
		ResyncEvery(1*time.Minute).
		ToController(controllerName, recorder.WithComponentSuffix(controllerName))
	return c
}

func (c *controller) Sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Infof("ControlPlaneNodeApprover: syncing")
	if err := c.updateNodeList(ctx); err != nil {
		return err
	}
	klog.Infof("ControlPlaneNodeApprover: new list of control plane nodes: %+v", c.readyControlPlaneNodesOnce)

	// Skip if key matches DefaultQueueKey (its not a CSR) or empty
	if syncCtx.QueueKey() == "" || syncCtx.QueueKey() == factory.DefaultQueueKey {
		return nil
	}

	err := c.syncCSR(ctx, syncCtx)
	if err != nil {
		klog.Infof("ControlPlaneNodeApprover: csr sync error: %w", err)
		return fmt.Errorf("ControlPlaneNodeApprover: %w", err)
	}
	return nil
}

func (c *controller) updateNodeList(ctx context.Context) error {
	nodeList, err := c.nodeGetter.Nodes().List(
		ctx, metav1.ListOptions{
			LabelSelector: labels.Set{"node-role.kubernetes.io/master": ""}.AsSelector().String(),
		})
	if err != nil {
		return fmt.Errorf("failed to list master nodes: %w", err)
	}
	// Don't recreate readyControlPlaneNodesOnce as we want to keep nodes which are now not ready (and thus not present in this map)
	if len(c.readyControlPlaneNodesOnce) == 0 {
		c.readyControlPlaneNodesOnce = make(map[string]NodeAddresses, 0)
	}
	for _, node := range nodeList.Items {
		if !checkNodeReady(&node) {
			continue
		}
		dnsAddresses, internalIPAddresses := getNodeAddresses(&node)
		c.readyControlPlaneNodesOnce[node.Name] = NodeAddresses{
			DNSNames:    dnsAddresses,
			IPAddresses: internalIPAddresses,
		}
	}
	return nil
}

func (c *controller) getUsernames() sets.String {
	userNames := sets.String{}
	for node := range c.readyControlPlaneNodesOnce {
		userNames = userNames.Insert(fmt.Sprintf("system:node:%s", node))
	}
	return userNames
}

func (c *controller) syncCSR(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Infof("ControlPlaneNodeApprover: sync queue, key: %v", syncCtx.QueueKey())
	csr, err := c.csrLister.Get(syncCtx.QueueKey())
	if err != nil {
		klog.Infof("ControlPlaneNodeApprover: error getting CSR from the queue: %w", err)
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	klog.Infof("ControlPlaneNodeApprover: syncing CSR %s", csr.Name)

	csrCopy := csr.DeepCopy()
	csrPEM, _ := pem.Decode(csr.Spec.Request)
	if csrPEM == nil {
		return fmt.Errorf("failed to PEM-parse the CSR block in .spec.request: no CSRs were found")
	}

	approved, denied := getCertApprovalCondition(&csrCopy.Status)
	if approved {
		return fmt.Errorf("CSR %s ignored: already approved", csrCopy.Name)
	}
	if denied {
		return fmt.Errorf("CSR %s ignored: already denied", csrCopy.Name)
	}

	x509CSR, err := x509.ParseCertificateRequest(csrPEM.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse the CSR bytes: %v", err)
	}

	if err = c.Validate(csr, x509CSR); err != nil {
		klog.Warningf("CSR %s ignored: %v", csrCopy.Name, err)
		return err
	}

	klog.Infof("ControlPlaneNodeApprover: Approving CSR %s", csrCopy.Name)
	csrCopy.Status.Conditions = append(csrCopy.Status.Conditions,
		certapiv1.CertificateSigningRequestCondition{
			Type:    certapiv1.CertificateApproved,
			Status:  corev1.ConditionTrue,
			Reason:  "AutoApproved",
			Message: fmt.Sprintf("Auto-approved CSR %q", csrCopy.Name),
		})
	syncCtx.Recorder().Eventf("CSRApproval", "The CSR %q has been approved", csrCopy.Name)
	_, err = c.csrClient.UpdateApproval(ctx, csrCopy.Name, csrCopy, metav1.UpdateOptions{})
	return err
}

func (c *controller) Validate(csrObj *certapiv1.CertificateSigningRequest, x509CSR *x509.CertificateRequest) error {
	if csrObj == nil || x509CSR == nil {
		return fmt.Errorf("received a 'nil' CSR")
	}

	klog.Infof("ControlPlaneNodeApprover: working on CSR %s", csrObj.Name)
	// Check username. It should either be a node bootstrapper or match one of known nodes
	if csrObj.Spec.Username != bootstrapperUsername && !c.getUsernames().Has(csrObj.Spec.Username) {
		return fmt.Errorf("CSR %q was created by an unexpected user: %q", csrObj.Name, csrObj.Spec.Username)
	}

	// Check groups
	expectedGroups := kubeletUserGroups.Clone()
	if csrObj.Spec.Username == bootstrapperUsername {
		expectedGroups = mcoUserGroups.Clone()
	}
	if csrGroups := sets.NewString(csrObj.Spec.Groups...); !csrGroups.Equal(expectedGroups) {
		return fmt.Errorf("CSR %q was created by a user with unexpected groups: %v", csrObj.Name, csrGroups.List())
	}

	// Get node node name from CN in the CSR
	commonNameSplit := strings.Split(x509CSR.Subject.CommonName, ":")
	nodeName := commonNameSplit[len(commonNameSplit)-1]
	var nodeAddresses NodeAddresses
	var ok bool
	if nodeAddresses, ok = c.readyControlPlaneNodesOnce[nodeName]; !ok {
		return fmt.Errorf("CSR %q CN has unexpected node name: %v", csrObj.Name, nodeName)
	}

	// Ensure dns names and IPs in SAN section of the CSR matches node data
	if c.getUsernames().Has(csrObj.Spec.Username) {
		if len(x509CSR.DNSNames) == 0 {
			return fmt.Errorf("expected CSR %q to have DNS names in SAN section but none found", csrObj.Name)
		}
		if !sets.NewString(x509CSR.DNSNames...).Equal(nodeAddresses.DNSNames) {
			return fmt.Errorf("expected CSR %q DNS names to be %v but got %v", c.readyControlPlaneNodesOnce, nodeAddresses.DNSNames, x509CSR.DNSNames)
		}
		if len(x509CSR.IPAddresses) == 0 {
			return fmt.Errorf("expected CSR %q to have IPs in SAN section but none found", csrObj.Name)
		}
		actualIPAddresses := ipAddressesToStringSet(x509CSR.IPAddresses)
		if !actualIPAddresses.Equal(nodeAddresses.IPAddresses) {
			return fmt.Errorf("expected CSR %q IP addresses to be %v but got %v", c.readyControlPlaneNodesOnce, nodeAddresses.IPAddresses, actualIPAddresses)
		}
	}
	return nil

}

func ipAddressesToStringSet(ips []net.IP) sets.String {
	result := sets.String{}
	for _, ip := range ips {
		result = result.Insert(ip.String())
	}
	return result
}

func checkNodeReady(node *corev1.Node) bool {
	for i := range node.Status.Conditions {
		cond := &node.Status.Conditions[i]
		// We consider the node for scheduling only when its:
		// - NodeReady condition status is ConditionTrue,
		// - NodeDiskPressure condition status is ConditionFalse,
		// - NodeNetworkUnavailable condition status is ConditionFalse.
		if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
			return false
		}
		if cond.Type == corev1.NodeDiskPressure && cond.Status != corev1.ConditionFalse {
			return false
		}
		if cond.Type == corev1.NodeNetworkUnavailable && cond.Status != corev1.ConditionFalse {
			return false
		}
	}
	// Ignore nodes that are marked unschedulable
	return !node.Spec.Unschedulable
}

func getNodeAddresses(node *corev1.Node) (sets.String, sets.String) {
	dnsAddresses := sets.String{}
	internalIPAddresses := sets.String{}
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeHostName {
			dnsAddresses = dnsAddresses.Insert(address.Address)
		}
		if address.Type == corev1.NodeInternalIP {
			internalIPAddresses = internalIPAddresses.Insert(address.Address)
		}
	}
	return dnsAddresses, internalIPAddresses
}

func getCertApprovalCondition(status *certapiv1.CertificateSigningRequestStatus) (approved bool, denied bool) {
	for _, c := range status.Conditions {
		if c.Type == certapiv1.CertificateApproved {
			approved = true
		}
		if c.Type == certapiv1.CertificateDenied {
			denied = true
		}
	}
	return
}
