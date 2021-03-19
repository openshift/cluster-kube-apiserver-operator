package boundsatokensignercontroller

import (
	"bytes"
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/encryption/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
)

const (
	operatorNamespace = operatorclient.OperatorNamespace
	targetNamespace   = operatorclient.TargetNamespace

	// A new keypair will first be written to this secret in the operator namespace...
	NextSigningKeySecretName = "next-bound-service-account-signing-key"
	// ...and will copied to this secret in the operand namespace once
	// it is safe to do so (i.e. public key present on master nodes).
	SigningKeySecretName = "bound-service-account-signing-key"
	PrivateKeyKey        = "service-account.key"
	PublicKeyKey         = "service-account.pub"

	PublicKeyConfigMapName = "bound-sa-token-signing-certs"
)

// BoundSATokenSignerController manages the keypair used to sign bound
// tokens and the key bundle used to verify them.
type BoundSATokenSignerController struct {
	operatorClient  v1helpers.StaticPodOperatorClient
	secretClient    corev1client.SecretsGetter
	configMapClient corev1client.ConfigMapsGetter
}

func NewBoundSATokenSignerController(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) factory.Controller {

	ret := &BoundSATokenSignerController{
		operatorClient:  operatorClient,
		secretClient:    v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		configMapClient: v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
	}

	return factory.New().WithInformers(
		kubeInformersForNamespaces.InformersFor(operatorNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(targetNamespace).Core().V1().ConfigMaps().Informer(),
		operatorClient.Informer(),
	).ResyncEvery(time.Minute).WithSync(ret.sync).ToController("BoundSATokenSignerController", eventRecorder)
}

func (c *BoundSATokenSignerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	syncMethods := []func(ctx context.Context, syncCtx factory.SyncContext) error{
		c.ensureNextOperatorSigningSecret,
		c.ensurePublicKeyConfigMap,
		c.ensureOperandSigningSecret,
	}
	errs := []error{}
	for _, syncMethod := range syncMethods {
		err := syncMethod(ctx, syncCtx)
		if err != nil {
			utilruntime.HandleError(err)
			errs = append(errs, err)
		}
	}
	return errorsutil.NewAggregate(errs)
}

// ensureNextOperatorSigningSecret ensures the existence of a secret in the operator
// namespace containing an RSA keypair used for signing and validating bound service
// account tokens.
func (c *BoundSATokenSignerController) ensureNextOperatorSigningSecret(ctx context.Context, syncCtx factory.SyncContext) error {
	// Attempt to retrieve the operator secret
	secret, err := c.secretClient.Secrets(operatorNamespace).Get(ctx, NextSigningKeySecretName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	// Create or update the secret if it is missing or lacks the expected keypair data
	needKeypair := false
	if secret == nil {
		klog.Error("Bound token signing keypair is missing and will be regenerated")
		needKeypair = true
	} else {
		err := crypto.CheckRSAKeyPair(secret.Data[PublicKeyKey], secret.Data[PrivateKeyKey])
		if err != nil {
			klog.Errorf("Bound token signing keypair is invalid and will be regenerated: %v", err)
			needKeypair = true
		}
	}
	if needKeypair {
		klog.V(2).Infof("Creating a new signing secret for bound service account tokens.")
		newSecret, err := newNextSigningSecret()
		if err != nil {
			return err
		}

		secret, _, err = resourceapply.ApplySecret(c.secretClient, syncCtx.Recorder(), newSecret)
		if err != nil {
			return err
		}
	}

	return nil
}

// ensurePublicKeyConfigMap ensures that the public key in the operator secret is
// present in the operand configmap. If the configmap is missing, it will be created
// with the current public key. If the configmap exists but does not contain the
// current public key, the key will be added.
func (c *BoundSATokenSignerController) ensurePublicKeyConfigMap(ctx context.Context, syncCtx factory.SyncContext) error {
	// Retrieve the operator secret that contains the current public key
	operatorSecret, err := c.secretClient.Secrets(operatorNamespace).Get(ctx, NextSigningKeySecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Retrieve the configmap that needs to contain the current public key
	cachedConfigMap, err := c.configMapClient.ConfigMaps(targetNamespace).Get(ctx, PublicKeyConfigMapName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	var configMap *corev1.ConfigMap
	if errors.IsNotFound(err) {
		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      PublicKeyConfigMapName,
			},
		}
	} else {
		// Make a copy to avoid mutating the cache
		configMap = cachedConfigMap.DeepCopy()
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}

	currPublicKey := string(operatorSecret.Data[PublicKeyKey])
	if currPublicKey == "" {
		return fmt.Errorf("no current %s found, one must be set in %s/%s secret", PublicKeyKey, operatorNamespace, NextSigningKeySecretName)
	}
	hasKey := configMapHasValue(configMap, currPublicKey)
	if !hasKey {
		// Increment until a unique name is found to ensure that the new public key
		// does not overwrite an existing one. Except where key revocation is
		// involved (which would require manual deletion of the verifying public
		// key), existing public keys in the configmap should be maintained to
		// minimize the potential for not being able to validate issued tokens.
		nextKeyIndex := len(configMap.Data) + 1
		nextKeyKey := ""
		for {
			possibleKey := fmt.Sprintf("service-account-%03d.pub", nextKeyIndex)
			_, ok := configMap.Data[possibleKey]
			if !ok {
				nextKeyKey = possibleKey
				break
			}
			nextKeyIndex += 1
		}

		// Ensure the configmap is updated with the current public key
		configMap.Data[nextKeyKey] = currPublicKey
		configMap, _, err = resourceapply.ApplyConfigMap(c.configMapClient, syncCtx.Recorder(), configMap)
		if err != nil {
			return err
		}
	}
	return nil
}

// ensureOperandSigningSecret ensures that the signing key secret in the operator
// namespace is copied to the operand namespace. If the operand secret is missing, it
// will be copied immediately to ensure the installer has something to deploy. If the
// operand secret already exists, it will only be updated once the associated public
// key has been synced to all master nodes to ensure that issued tokens can be
// verified by all apiservers.
func (c *BoundSATokenSignerController) ensureOperandSigningSecret(ctx context.Context, syncCtx factory.SyncContext) error {
	// Retrieve the operator signing secret
	operatorSecret, err := c.secretClient.Secrets(operatorNamespace).Get(ctx, NextSigningKeySecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Retrieve the operand signing secret
	operandSecret, err := c.secretClient.Secrets(targetNamespace).Get(ctx, SigningKeySecretName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	// If operand secret matches the operator secret, all done
	operandSecretUpToDate := (operandSecret != nil &&
		bytes.Equal(operandSecret.Data[PublicKeyKey], operatorSecret.Data[PublicKeyKey]) &&
		bytes.Equal(operandSecret.Data[PrivateKeyKey], operatorSecret.Data[PrivateKeyKey]))
	if operandSecretUpToDate {
		return nil
	}

	currPublicKey := string(operatorSecret.Data[PublicKeyKey])

	// The current public key must be present in the configmap before ensuring that
	// the operand secret matches the operator secret to avoid apiservers that can
	// issue tokens that can't be validated.
	configMap, err := c.configMapClient.ConfigMaps(targetNamespace).Get(ctx, PublicKeyConfigMapName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !configMapHasValue(configMap, currPublicKey) {
		return fmt.Errorf("unable to promote bound sa token signing key until public key configmap has been updated")
	}

	syncAllowed := false

	if operandSecret == nil {
		// If the operand secret is missing, it must be created to ensure the
		// installer can proceed regardless of whether public keys have already been
		// synced to the master nodes.
		syncAllowed = true
	} else {
		// Update the operand secret only if the current public key has been synced to
		// all nodes.
		syncAllowed, err = c.publicKeySyncedToAllNodes(ctx, currPublicKey)
		if err != nil {
			return err
		}
		if syncAllowed {
			klog.V(2).Info("Promoting the secret containing the keypair used to sign bound service account tokens to the operand namespace.")
		} else {
			klog.V(2).Info("Promotion of the secret containing the keypair used to sign bound service account tokens is pending distribution of its public key to master nodes.")
		}
	}
	if !syncAllowed {
		return nil
	}
	_, _, err = resourceapply.SyncSecret(c.secretClient, syncCtx.Recorder(),
		operatorNamespace, NextSigningKeySecretName,
		targetNamespace, SigningKeySecretName, []metav1.OwnerReference{})
	return err
}

// publicKeySyncedToAllNodes indicates whether the given public key is present on the
// current revisions of the apiserver nodes by checking for the key with the
// configmaps associated with those revisions.
func (c *BoundSATokenSignerController) publicKeySyncedToAllNodes(ctx context.Context, publicKey string) (bool, error) {
	_, operatorStatus, _, err := c.operatorClient.GetStaticPodOperatorState()
	if err != nil {
		return false, err
	}

	// Collect the unique set of revisions of the apiserver nodes
	revisionMap := map[int32]struct{}{}
	uniqueRevisions := []int32{}
	for _, nodeStatus := range operatorStatus.NodeStatuses {
		revision := nodeStatus.CurrentRevision
		if _, ok := revisionMap[revision]; !ok {
			revisionMap[revision] = struct{}{}
			uniqueRevisions = append(uniqueRevisions, revision)
		}
	}

	// For each revision, check that the configmap for that revision contains the
	// current public key. If any configmap for any given revision is missing or does
	// not contain the public key, assume the public key is not present on that node.
	for _, revision := range uniqueRevisions {
		configMapNameWithRevision := fmt.Sprintf("%s-%d", PublicKeyConfigMapName, revision)
		configMap, err := c.configMapClient.ConfigMaps(operatorclient.TargetNamespace).Get(ctx, configMapNameWithRevision, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if !configMapHasValue(configMap, publicKey) {
			return false, nil
		}
	}

	return true, nil
}

// newNextSigningSecret creates a new secret populated with a new keypair.
func newNextSigningSecret() (*corev1.Secret, error) {
	publicBytes, privateBytes, err := crypto.GenerateRSAKeyPair()
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: operatorNamespace,
			Name:      NextSigningKeySecretName,
		},
		Data: map[string][]byte{
			PrivateKeyKey: privateBytes,
			PublicKeyKey:  publicBytes,
		},
	}, nil
}

func configMapHasValue(configMap *corev1.ConfigMap, desiredValue string) bool {
	for _, value := range configMap.Data {
		if value == desiredValue {
			return true
		}
	}
	return false
}
