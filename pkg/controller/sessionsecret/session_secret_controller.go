package sessionsecret

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang/glog"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	informers "k8s.io/client-go/informers/core/v1"
	kcoreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	sessionSecretNamespace = "openshift-kube-apiserver"
	sessionSecretName      = "session-secret"
)

// SessionSecrets struct is copied from github.com/openshift/api/legacyconfig/v1 so we can manually encode and not rely
// on that package.
type SessionSecrets struct {
	metav1.TypeMeta `json:",inline"`

	// Secrets is a list of secrets
	// New sessions are signed and encrypted using the first secret.
	// Existing sessions are decrypted/authenticated by each secret until one succeeds. This allows rotating secrets.
	Secrets []SessionSecret `json:"secrets"`
}

// SessionSecret is a secret used to authenticate/decrypt cookie-based sessions
type SessionSecret struct {
	// Authentication is used to authenticate sessions using HMAC. Recommended to use a secret with 32 or 64 bytes.
	Authentication string `json:"authentication"`
	// Encryption is used to encrypt sessions. Must be 16, 24, or 32 characters long, to select AES-128, AES-
	Encryption string `json:"encryption"`
}

// Taken from origin but could be moved to library-go
const (
	sha256KeyLenBits = sha256.BlockSize * 8 // max key size with HMAC SHA256
	aes256KeyLenBits = 256                  // max key size with AES (AES-256)
)

func randomAuthKeyBits() []byte {
	return randomBits(sha256KeyLenBits)
}

func randomEncKeyBits() []byte {
	return randomBits(aes256KeyLenBits)
}

// randomBits returns a random byte slice with at least the requested bits of entropy.
// Callers should avoid using a value less than 256 unless they have a very good reason.
func randomBits(bits int) []byte {
	size := bits / 8
	if bits%8 != 0 {
		size++
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}

type SessionSecretController struct {
	secretLister listers.SecretLister
	secretClient kcoreclient.SecretsGetter

	secretsHasSynced cache.InformerSynced
	syncHandler      func(serviceKey string) error

	secretsQueue  workqueue.RateLimitingInterface
	eventRecorder events.Recorder
}

func NewSessionSecretController(secrets informers.SecretInformer, secretsClient kcoreclient.SecretsGetter, resyncInterval time.Duration, eventRecorder events.Recorder) *SessionSecretController {
	sc := &SessionSecretController{
		secretsQueue: workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
	}

	sc.secretLister = secrets.Lister()

	secrets.Informer().AddEventHandlerWithResyncPeriod(
		cache.FilteringResourceEventHandler{
			FilterFunc: isSessionSecret,
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc: sc.enqueueSecret,
			},
		},
		resyncInterval,
	)

	sc.secretClient = secretsClient
	sc.secretsHasSynced = secrets.Informer().HasSynced

	sc.syncHandler = sc.syncSecret
	sc.eventRecorder = eventRecorder

	return sc
}

// Run begins watching and syncing.
func (sc *SessionSecretController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer sc.secretsQueue.ShutDown()

	// Wait for the stores to fill
	if !cache.WaitForCacheSync(stopCh, sc.secretsHasSynced) {
		return
	}

	glog.V(4).Infof("Starting workers for SessionSecretController")
	for i := 0; i < workers; i++ {
		go wait.Until(sc.runWorker, time.Second, stopCh)
	}

	// Add a queue item to trigger initial creation of the secret
	sc.enqueueSecret(nil)

	<-stopCh
	glog.V(4).Infof("Shutting down SessionSecretController")
}

// processNextWorkItem deals with one key off the secretsQueue.  It returns false when it's time to quit.
func (sc *SessionSecretController) processNextWorkItem() bool {
	key, quit := sc.secretsQueue.Get()
	if quit {
		return false
	}
	defer sc.secretsQueue.Done(key)

	err := sc.syncHandler(key.(string))
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("%v failed with : %v", key, err))
		sc.eventRecorder.Warningf("CreateSessionSecretFailure", "%v failed with : %v", key, err)
		sc.secretsQueue.AddRateLimited(key)
		return true
	}

	sc.secretsQueue.Forget(key)
	return true
}

func (sc *SessionSecretController) runWorker() {
	for sc.processNextWorkItem() {
	}
}

func newSessionSecretsJSON() ([]byte, error) {
	secrets := &SessionSecrets{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SessionSecrets",
			APIVersion: "v1",
		},
		Secrets: []SessionSecret{
			{
				Authentication: string(randomAuthKeyBits()),
				Encryption:     string(randomEncKeyBits()),
			},
		},
	}
	return json.Marshal(secrets)
}

func (sc *SessionSecretController) createSessionSecret() error {
	secretsBytes, err := newSessionSecretsJSON()
	if err != nil {
		return err
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sessionSecretName,
			Namespace: sessionSecretNamespace,
		},
		Data: map[string][]byte{
			"secrets": secretsBytes,
		},
	}
	_, err = sc.secretClient.Secrets(secret.Namespace).Create(secret)
	if errors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// syncSecret creates the session secret if it doesn't exist.
func (sc *SessionSecretController) syncSecret(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	_, err = sc.secretLister.Secrets(namespace).Get(name)
	if errors.IsNotFound(err) {
		glog.V(4).Infof("creating secret %s/%s", namespace, name)
		return sc.createSessionSecret()
	}
	if err != nil {
		return err
	}
	return nil
}

func (sc *SessionSecretController) enqueueSecret(obj interface{}) {
	sc.secretsQueue.Add(sessionSecretNamespace + "/" + sessionSecretName)
}

func isSessionSecret(obj interface{}) bool {
	secret, ok := obj.(*v1.Secret)
	if !ok {
		return false
	}
	return secret.Namespace == sessionSecretNamespace && secret.Name == sessionSecretName
}
