package nodekubeconfigcontroller

import (
	"context"
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/openshift/api/annotations"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const (
	workQueueKey          = "key"
	kubeApiserverServerCA = "kube-apiserver-server-ca"
	nodeSystemAdminClient = "node-system-admin-client"
)

type NodeKubeconfigController struct {
	operatorClient v1helpers.StaticPodOperatorClient

	kubeClient           kubernetes.Interface
	configMapLister      corev1listers.ConfigMapLister
	secretLister         corev1listers.SecretLister
	infrastructureLister configv1listers.InfrastructureLister
}

func NewNodeKubeconfigController(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	infrastructureInformer configv1informers.InfrastructureInformer,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &NodeKubeconfigController{
		operatorClient:       operatorClient,
		kubeClient:           kubeClient,
		configMapLister:      kubeInformersForNamespaces.ConfigMapLister(),
		secretLister:         kubeInformersForNamespaces.SecretLister(),
		infrastructureLister: infrastructureInformer.Lister(),
	}

	return factory.New().WithFilteredEventsInformers(
		func(obj interface{}) bool {
			if cm, ok := obj.(*corev1.ConfigMap); ok {
				if cm.Namespace == operatorclient.TargetNamespace && cm.Name == kubeApiserverServerCA {
					return true
				}
				return false
			}
			if secret, ok := obj.(*corev1.Secret); ok {
				if secret.Namespace == operatorclient.OperatorNamespace && secret.Name == nodeSystemAdminClient {
					return true
				}
				return false
			}
			return true
		},
		operatorClient.Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Informer(),
		infrastructureInformer.Informer(),
	).WithSync(c.sync).WithSyncDegradedOnError(c.operatorClient).ResyncEvery(5*time.Minute).ToController("NodeKubeconfigController", eventRecorder.WithComponentSuffix("node-kubeconfig-controller"))
}

func (c NodeKubeconfigController) sync(ctx context.Context, syncContext factory.SyncContext) error {
	operatorSpec, _, _, err := c.operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return err
	}

	switch operatorSpec.ManagementState {
	case operatorv1.Managed:
	case operatorv1.Unmanaged:
		return nil
	case operatorv1.Removed:
		// TODO probably just fail
		return nil
	default:
		syncContext.Recorder().Warningf("ManagementStateUnknown", "Unrecognized operator management state %q", operatorSpec.ManagementState)
		return nil
	}

	var errors []error

	err = ensureNodeKubeconfigs(
		ctx,
		c.kubeClient.CoreV1(),
		c.secretLister,
		c.configMapLister,
		c.infrastructureLister,
		syncContext.Recorder(),
	)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "secret/node-kubeconfigs", err))
	}

	return v1helpers.NewMultiLineAggregate(errors)
}

func ensureNodeKubeconfigs(ctx context.Context, client coreclientv1.CoreV1Interface, secretLister corev1listers.SecretLister, configmapLister corev1listers.ConfigMapLister, infrastructureLister configv1listers.InfrastructureLister, recorder events.Recorder) error {
	requiredSecret := resourceread.ReadSecretV1OrDie(bindata.MustAsset("assets/kube-apiserver/node-kubeconfigs.yaml"))

	systemAdminCredsSecret, err := secretLister.Secrets(operatorclient.OperatorNamespace).Get(nodeSystemAdminClient)
	if err != nil {
		return err
	}

	systemAdminClientCert := systemAdminCredsSecret.Data[corev1.TLSCertKey]
	if len(systemAdminClientCert) == 0 {
		return fmt.Errorf("system:admin client certificate missing from secret %s/%s", operatorclient.OperatorNamespace, nodeSystemAdminClient)
	}
	systemAdminClientKey := systemAdminCredsSecret.Data[corev1.TLSPrivateKeyKey]
	if len(systemAdminClientKey) == 0 {
		return fmt.Errorf("system:admin client private key missing from secret %s/%s", operatorclient.OperatorNamespace, nodeSystemAdminClient)
	}

	servingCABundleCM, err := configmapLister.ConfigMaps(operatorclient.TargetNamespace).Get(kubeApiserverServerCA)
	if err != nil {
		return err
	}
	servingCABundleData := servingCABundleCM.Data["ca-bundle.crt"]
	if len(servingCABundleData) == 0 {
		return fmt.Errorf("serving CA bundle missing from configmap %s/%s", operatorclient.TargetNamespace, kubeApiserverServerCA)
	}

	infrastructure, err := infrastructureLister.Get("cluster")
	if err != nil {
		return err
	}
	apiServerInternalURL := infrastructure.Status.APIServerInternalURL
	if len(apiServerInternalURL) == 0 {
		return fmt.Errorf("APIServerInternalURL missing from infrastructure/cluster")
	}
	apiServerURL := infrastructure.Status.APIServerURL
	if len(apiServerURL) == 0 {
		return fmt.Errorf("APIServerURL missing from infrastructure/cluster")
	}

	for k, data := range requiredSecret.StringData {
		for pattern, replacement := range map[string]string{
			"$LB-INT":                 apiServerInternalURL,
			"$LB-EXT":                 apiServerURL,
			"$CA_DATA":                base64.StdEncoding.EncodeToString([]byte(servingCABundleData)),
			"$SYSTEM_ADMIN_CERT_DATA": base64.StdEncoding.EncodeToString(systemAdminClientCert),
			"$SYSTEM_ADMIN_KEY_DATA":  base64.StdEncoding.EncodeToString(systemAdminClientKey),
		} {
			data = strings.ReplaceAll(data, pattern, replacement)
		}

		requiredSecret.StringData[k] = data
	}

	if requiredSecret.Annotations == nil {
		requiredSecret.Annotations = map[string]string{}
	}
	requiredSecret.Annotations[annotations.OpenShiftComponent] = "kube-apiserver"
	// Copy not-before/not-after annotations from systemAdminClientCert
	if len(systemAdminCredsSecret.Annotations[certrotation.CertificateNotBeforeAnnotation]) > 0 {
		requiredSecret.Annotations[certrotation.CertificateNotBeforeAnnotation] = systemAdminCredsSecret.Annotations[certrotation.CertificateNotBeforeAnnotation]
	}
	if len(systemAdminCredsSecret.Annotations[certrotation.CertificateNotAfterAnnotation]) > 0 {
		requiredSecret.Annotations[certrotation.CertificateNotAfterAnnotation] = systemAdminCredsSecret.Annotations[certrotation.CertificateNotAfterAnnotation]
	}

	actualSecret, err := secretLister.Secrets(requiredSecret.Namespace).Get(requiredSecret.Name)
	if !apierrors.IsNotFound(err) {
		if err != nil {
			return err
		}
		if reflect.DeepEqual(actualSecret.Data, requiredSecret.Data) && reflect.DeepEqual(actualSecret.Annotations, requiredSecret.Annotations) {
			return nil
		}
	}
	_, _, err = resourceapply.ApplySecret(ctx, client, recorder, requiredSecret)
	return err
}
