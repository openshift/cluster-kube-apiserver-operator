package serviceaccountissuercontroller

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	operatorinformers "github.com/openshift/client-go/operator/informers/externalversions"
	operatorlistersv1 "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

const defaultTrustedServiceAccountIssuerExpirationDuration = 24 * time.Hour

// ServiceAccountIssuerController synchronize the authentication.config.openshift.io serviceAccountIssuer field value
// into a kubeapiserver.operator.openshift.io status field.
// The purpose of this controller is to keep the previous values stored and used as "trusted" service account issuers.
// Doing this allows cluster to smoothly transition from one service account issuer to other issuer.
type ServiceAccountIssuerController struct {
	kubeAPIServerOperatorClient operatorv1client.KubeAPIServerInterface
	authLister                  configlistersv1.AuthenticationLister
	kubeAPIserverOperatorLister operatorlistersv1.KubeAPIServerLister

	// unit testing
	nowFn func() time.Time
}

func NewController(kubeAPIServerOperatorClient operatorv1client.KubeAPIServerInterface, operatorInformers operatorinformers.SharedInformerFactory, configInformer configinformers.SharedInformerFactory, eventRecorder events.Recorder) factory.Controller {
	var ret = &ServiceAccountIssuerController{
		nowFn:                       time.Now,
		kubeAPIServerOperatorClient: kubeAPIServerOperatorClient,
		authLister:                  configInformer.Config().V1().Authentications().Lister(),
		kubeAPIserverOperatorLister: operatorInformers.Operator().V1().KubeAPIServers().Lister(),
	}
	return factory.New().WithInformers(
		operatorInformers.Operator().V1().KubeAPIServers().Informer(),
		configInformer.Config().V1().Authentications().Informer(),
	).ResyncEvery(30*time.Second).WithSync(ret.sync).ToController("ServiceAccountIssuerController", eventRecorder)
}

func (c *ServiceAccountIssuerController) sync(ctx context.Context, controllerContext factory.SyncContext) error {
	authConfig, err := c.authLister.Get("cluster")
	if err != nil {
		return err
	}
	currentIssuer := authConfig.Spec.ServiceAccountIssuer

	operator, err := c.kubeAPIserverOperatorLister.Get("cluster")
	if err != nil {
		return err
	}

	// there is no service account issuer set and there are no service account issuers in status, no-op.
	if len(currentIssuer) == 0 && len(operator.Status.ServiceAccountIssuers) == 0 {
		return nil
	}

	// if the status is not set, but the current issuer is not empty, update the status and let the controller resync
	if len(currentIssuer) > 0 && len(operator.Status.ServiceAccountIssuers) == 0 {
		operatorCopy := operator.DeepCopy()
		operatorCopy.Status.ServiceAccountIssuers = []operatorv1.ServiceAccountIssuerStatus{
			{
				Name: currentIssuer,
			},
		}
		_, statusUpdateErr := c.kubeAPIServerOperatorClient.UpdateStatus(ctx, operatorCopy, metav1.UpdateOptions{})
		if statusUpdateErr == nil {
			klog.Infof("ServiceAccountIssuer %q is now set as active issuer", currentIssuer)
			statusUpdateErr = factory.SyntheticRequeueError
		}
		return statusUpdateErr
	}

	// in this case, user deleted service account issuer value, which means we have to remove all values from the status
	// if we don't do this and instead just remove first value, the new tokens will be issued with "previous" service
	// account issuer which is not desired
	if len(currentIssuer) == 0 && len(operator.Status.ServiceAccountIssuers) != 0 {
		operatorCopy := operator.DeepCopy()
		operatorCopy.Status.ServiceAccountIssuers = []operatorv1.ServiceAccountIssuerStatus{}
		_, statusUpdateErr := c.kubeAPIServerOperatorClient.UpdateStatus(ctx, operatorCopy, metav1.UpdateOptions{})
		if statusUpdateErr == nil {
			klog.Infof("ServiceAccountIssuer value is empty, removing all previously used trusted issuers")
			statusUpdateErr = factory.SyntheticRequeueError
		}
		return statusUpdateErr
	}

	activeIssuer := getActiveServiceAccountIssuer(operator.Status.ServiceAccountIssuers)
	if len(activeIssuer) == 0 {
		// at this point, we must have the active issuer (the one without expiration time).
		// if we don't it means somebody changed the status deliberately.
		// in this case, we correct it by setting the configured value as active.
		// NOTE: this is an error/edge case
		return c.makeActiveIssuerTrusted(ctx, currentIssuer, currentIssuer, operator)
	}

	issuerChanged := currentIssuer != activeIssuer

	// the issuer configured in auth config and the active issuer we have in operator status matches.
	// this is no-op configuration wise, but we prune the list from expired issuers.
	if !issuerChanged {
		if pruned, err := c.pruneExpiredServiceAccountIssuers(ctx, operator); err != nil {
			if err == factory.SyntheticRequeueError {
				controllerContext.Recorder().Eventf("ServiceAccountIssuer",
					"The following service account issuers were pruned and are no longer trusted: %s", strings.Join(pruned, ","),
				)
			}
			return err
		}
		return nil
	}

	// the last case is when the current issuer does not match the active issuer.
	// that means user changed the value in auth config and we need to make the active issuer "trusted".
	// trusted issuers have expiration time set and they are going to be pruned by this controller when the expiration
	// timeout.
	if err := c.makeActiveIssuerTrusted(ctx, activeIssuer, currentIssuer, operator); err != nil {
		// Successful issuer change is event worthy.
		if err == factory.SyntheticRequeueError {
			controllerContext.Recorder().Eventf("ServiceAccountIssuer",
				"Desired ServiceAccountIssuer %q is now active issuer. Previous issuer %q is trusted until %s",
				currentIssuer, activeIssuer, c.nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration),
			)
		}
		return err
	}
	return nil
}

// getActiveServiceAccountIssuer gets the active (currently used to generate new bound tokens) issuer.
// This is the issuer without expiration time set.
func getActiveServiceAccountIssuer(issuers []operatorv1.ServiceAccountIssuerStatus) string {
	for i := range issuers {
		if issuers[i].ExpirationTime == nil {
			return issuers[i].Name
		}
	}
	return ""
}

// makeActiveIssuerTrusted puts the issuer configured by the user in authentication.config.openshift.io into the list in
// KAS-O status field. The previously used service account issuer will get expiration set.
func (c *ServiceAccountIssuerController) makeActiveIssuerTrusted(ctx context.Context, oldIssuer, newIssuer string, server *operatorv1.KubeAPIServer) error {
	updated := []operatorv1.ServiceAccountIssuerStatus{
		{
			Name: newIssuer,
		},
	}
	for i := range server.Status.ServiceAccountIssuers {
		if server.Status.ServiceAccountIssuers[i].ExpirationTime == nil && server.Status.ServiceAccountIssuers[i].Name == oldIssuer {
			expiration := metav1.Time{Time: c.nowFn().Add(defaultTrustedServiceAccountIssuerExpirationDuration)}
			updated = append(updated, operatorv1.ServiceAccountIssuerStatus{
				Name:           oldIssuer,
				ExpirationTime: &expiration,
			})
			continue
		}
		// handle the case when new issuer is already in the trusted list
		// this will remove it from the list
		if server.Status.ServiceAccountIssuers[i].Name == newIssuer {
			continue
		}
		updated = append(updated, server.Status.ServiceAccountIssuers[i])
	}
	if len(updated) > 10 {
		return fmt.Errorf("unable to configure more than 10 trusted service account issuers at the time, please wait until old issuers expire")
	}
	serverCopy := server.DeepCopy()
	serverCopy.Status.ServiceAccountIssuers = updated
	_, err := c.kubeAPIServerOperatorClient.UpdateStatus(ctx, serverCopy, metav1.UpdateOptions{})
	// the error means the status changed, instead of waiting for informer to update, trigger resync immediately.
	if err == nil {
		return factory.SyntheticRequeueError
	}
	return err
}

// pruneExpiredServiceAccountIssuers prunes the expired service account issuers from status field.
func (c *ServiceAccountIssuerController) pruneExpiredServiceAccountIssuers(ctx context.Context, server *operatorv1.KubeAPIServer) ([]string, error) {
	var (
		issuersToKeep  []operatorv1.ServiceAccountIssuerStatus
		removedIssuers []string
	)
	for i := range server.Status.ServiceAccountIssuers {
		// keep the active issuer and the issuers that has not expired
		if server.Status.ServiceAccountIssuers[i].ExpirationTime == nil || server.Status.ServiceAccountIssuers[i].ExpirationTime.Time.After(c.nowFn()) {
			issuersToKeep = append(issuersToKeep, server.Status.ServiceAccountIssuers[i])
			continue
		}
		removedIssuers = append(removedIssuers, server.Status.ServiceAccountIssuers[i].Name)
	}
	if len(removedIssuers) == 0 {
		return nil, nil
	}

	serverCopy := server.DeepCopy()
	serverCopy.Status.ServiceAccountIssuers = issuersToKeep
	_, err := c.kubeAPIServerOperatorClient.UpdateStatus(ctx, serverCopy, metav1.UpdateOptions{})
	// the error means the status changed, instead of waiting for informer to update, trigger resync immediately.
	if err == nil {
		return removedIssuers, factory.SyntheticRequeueError
	}
	return nil, err
}
