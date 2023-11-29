package webhooksupportabilitycontroller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/klog/v2"
)

// webhookInfo generically represents a webhook
type webhookInfo struct {
	Name                  string
	Service               *serviceReference
	CABundle              []byte
	FailurePolicyIsIgnore bool
	// TimeoutSeconds specifies the timeout for a webhook.
	// After the timeout passes, the webhook call will be ignored or the API call will fail
	TimeoutSeconds *int32
}

// serviceReference generically represents a service reference
type serviceReference struct {
	Namespace string
	Name      string
	Port      *int32
}

// updateWebhookConfigurationDegraded updates the condition specified after
// checking that the services associated with the specified webhooks exist
// and have at least one ready endpoint.
func (c *webhookSupportabilityController) updateWebhookConfigurationDegraded(ctx context.Context, condition operatorv1.OperatorCondition, webhookInfos []webhookInfo) v1helpers.UpdateStatusFunc {
	var serviceMsgs []string
	var tlsMsgs []string
	for _, webhook := range webhookInfos {
		if webhook.Service != nil {
			err := c.assertService(webhook.Service)
			if err != nil {
				msg := fmt.Sprintf("%s: %s", webhook.Name, err)
				if webhook.FailurePolicyIsIgnore {
					klog.Error(msg)
					continue
				}
				serviceMsgs = append(serviceMsgs, msg)
				continue
			}
			err = c.assertConnect(ctx, webhook.Name, webhook.Service, webhook.CABundle, webhook.TimeoutSeconds)
			if err != nil {
				msg := fmt.Sprintf("%s: %s", webhook.Name, err)
				if webhook.FailurePolicyIsIgnore {
					klog.Error(msg)
					continue
				}
				tlsMsgs = append(tlsMsgs, msg)
				continue
			}
		}
	}

	svc, tls := len(serviceMsgs) > 0, len(tlsMsgs) > 0
	switch {
	case svc && tls:
		condition.Reason = WebhookServiceNotReadyReason
		condition.Status = operatorv1.ConditionTrue
	case svc:
		condition.Reason = WebhookServiceNotFoundReason
		condition.Status = operatorv1.ConditionTrue
	case tls:
		condition.Reason = WebhookServiceConnectionErrorReason
		condition.Status = operatorv1.ConditionTrue
	default:
		condition.Reason = ""
		condition.Status = operatorv1.ConditionFalse
	}
	msgs := append(serviceMsgs, tlsMsgs...)
	sort.Strings(msgs)
	condition.Message = strings.Join(msgs, "\n")

	return v1helpers.UpdateConditionFn(condition)
}

// assertService checks that the referenced service resource exists.
func (c *webhookSupportabilityController) assertService(reference *serviceReference) error {
	_, err := c.serviceLister.Services(reference.Namespace).Get(reference.Name)
	if err != nil {
		return fmt.Errorf("unable to find service %s.%s: %v", reference.Name, reference.Namespace, err)
	}
	return nil
}

// assertConnect performs a dns lookup of service, opens a tcp connection, and performs a tls handshake.
func (c *webhookSupportabilityController) assertConnect(ctx context.Context, webhookName string, reference *serviceReference, caBundle []byte, webhookTimeoutSeconds *int32) error {
	host := reference.Name + "." + reference.Namespace + ".svc"
	port := "443"
	address := net.JoinHostPort(host, port)
	if reference.Port != nil {
		port = fmt.Sprintf("%d", *reference.Port)
	}
	rootCAs := x509.NewCertPool()
	if len(caBundle) > 0 {
		rootCAs.AppendCertsFromPEM(caBundle)
	}
	timeout := 10 * time.Second
	if webhookTimeoutSeconds != nil {
		timeout = time.Duration(*webhookTimeoutSeconds) * time.Second
	}
	// the last error that occurred in the loop below
	var err error
	// retry up to 3 times on error
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(i) * time.Second):
		}
		dialer := &net.Dialer{
			Timeout: timeout,
		}
		var conn net.Conn
		conn, err = dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			if i != 2 {
				// log warning since only last one is reported
				klog.V(4).InfoS("Could not open TCP connection to webhook", "webhook", webhookName, "address", address, "error", err)
			}
			err = fmt.Errorf("could not connect to webhook: %w", err)
			continue
		}

		// perform tls handshake to avoid spamming the logs of webhooks
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host, RootCAs: rootCAs})
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			if i != 2 {
				// log warning since only last one is reported
				klog.V(4).InfoS("Could not perform TLS handshake over connection to webhook", "webhook", webhookName, "address", address, "error", err)
			}
			_ = tlsConn.Close()
			err = fmt.Errorf("could not perform tls handshake over connection to webhook: %w", err)
			continue
		}

		// error from closing connection should not affect Degraded condition
		_ = tlsConn.Close()
		break
	}
	return err
}
