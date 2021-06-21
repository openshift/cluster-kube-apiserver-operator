package targetconfigcontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/ghodss/yaml"

	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/v410_00_assets"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type TargetConfigController struct {
	targetImagePullSpec   string
	operatorImagePullSpec string

	operatorClient v1helpers.StaticPodOperatorClient

	kubeClient      kubernetes.Interface
	configMapLister corev1listers.ConfigMapLister

	enableRolloutMonitor bool
}

func NewTargetConfigController(
	targetImagePullSpec, operatorImagePullSpec string,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForOpenshiftKubeAPIServerNamespace informers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	enableRolloutMonitor bool,
) factory.Controller {
	c := &TargetConfigController{
		targetImagePullSpec:   targetImagePullSpec,
		operatorImagePullSpec: operatorImagePullSpec,
		operatorClient:        operatorClient,
		kubeClient:            kubeClient,
		configMapLister:       kubeInformersForNamespaces.ConfigMapLister(),
		enableRolloutMonitor:  enableRolloutMonitor,
	}

	return factory.New().WithInformers(
		operatorClient.Informer(),
		kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().ConfigMaps().Informer(),
		kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().Secrets().Informer(),
		kubeInformersForOpenshiftKubeAPIServerNamespace.Core().V1().ServiceAccounts().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Informer(),
	).WithSync(c.sync).ResyncEvery(time.Minute).ToController("TargetConfigController", eventRecorder.WithComponentSuffix("target-config-controller"))
}

func (c TargetConfigController) sync(ctx context.Context, syncContext factory.SyncContext) error {
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

	// block until config is observed and specific paths are present
	if err := isRequiredConfigPresent(operatorSpec.ObservedConfig.Raw); err != nil {
		syncContext.Recorder().Warning("ConfigMissing", err.Error())
		return err
	}

	requeue, err := createTargetConfig(ctx, c, syncContext.Recorder(), operatorSpec)
	if err != nil {
		return err
	}
	if requeue {
		return factory.SyntheticRequeueError
	}

	return nil
}

func isRequiredConfigPresent(config []byte) error {
	if len(config) == 0 {
		return fmt.Errorf("no observedConfig")
	}

	existingConfig := map[string]interface{}{}
	if err := json.NewDecoder(bytes.NewBuffer(config)).Decode(&existingConfig); err != nil {
		return fmt.Errorf("error parsing config, %v", err)
	}

	requiredPaths := [][]string{
		{"servingInfo", "namedCertificates"},
		{"apiServerArguments", "etcd-servers"},
		{"admission", "pluginConfig", "network.openshift.io/RestrictedEndpointsAdmission"},
	}
	for _, requiredPath := range requiredPaths {
		configVal, found, err := unstructured.NestedFieldNoCopy(existingConfig, requiredPath...)
		if err != nil {
			return fmt.Errorf("error reading %v from config, %v", strings.Join(requiredPath, "."), err)
		}
		if !found {
			return fmt.Errorf("%v missing from config", strings.Join(requiredPath, "."))
		}
		if configVal == nil {
			return fmt.Errorf("%v null in config", strings.Join(requiredPath, "."))
		}
		if configValSlice, ok := configVal.([]interface{}); ok && len(configValSlice) == 0 {
			return fmt.Errorf("%v empty in config", strings.Join(requiredPath, "."))
		}
		if configValString, ok := configVal.(string); ok && len(configValString) == 0 {
			return fmt.Errorf("%v empty in config", strings.Join(requiredPath, "."))
		}
	}
	return nil
}

// createTargetConfig takes care of creation of valid resources in a fixed name.  These are inputs to other control loops.
// returns whether or not requeue and if an error happened when updating status.  Normally it updates status itself.
func createTargetConfig(ctx context.Context, c TargetConfigController, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec) (bool, error) {
	errors := []error{}

	_, _, err := manageKubeAPIServerConfig(c.kubeClient.CoreV1(), recorder, operatorSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/config", err))
	}
	_, _, err = managePod(c.kubeClient.CoreV1(), recorder, operatorSpec, c.targetImagePullSpec, c.operatorImagePullSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-apiserver-pod", err))
	}
	_, _, err = ManageClientCABundle(c.configMapLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/client-ca", err))
	}
	_, _, err = manageKubeAPIServerCABundle(c.configMapLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-apiserver-server-ca", err))
	}

	err = ensureKubeAPIServerTrustedCA(ctx, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/trusted-ca-bundle", err))
	}

	err = ensureLocalhostRecoverySAToken(ctx, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "serviceaccount/localhost-recovery-client", err))
	}

	if c.enableRolloutMonitor {
		_, _, err = manageRolloutMonitorPod(c.kubeClient.CoreV1(), recorder, c.operatorImagePullSpec)
		if err != nil {
			errors = append(errors, fmt.Errorf("%q: %v", "configmap/rollout-monitor-pod", err))
		}
	}

	if len(errors) > 0 {
		condition := operatorv1.OperatorCondition{
			Type:    "TargetConfigControllerDegraded",
			Status:  operatorv1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: v1helpers.NewMultiLineAggregate(errors).Error(),
		}
		if _, _, err := v1helpers.UpdateStaticPodStatus(c.operatorClient, v1helpers.UpdateStaticPodConditionFn(condition)); err != nil {
			return true, err
		}
		return true, nil
	}

	condition := operatorv1.OperatorCondition{
		Type:   "TargetConfigControllerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if _, _, err := v1helpers.UpdateStaticPodStatus(c.operatorClient, v1helpers.UpdateStaticPodConditionFn(condition)); err != nil {
		return true, err
	}

	return false, nil
}

func manageKubeAPIServerConfig(client coreclientv1.ConfigMapsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/cm.yaml"))
	defaultConfig := v410_00_assets.MustAsset("v4.1.0/config/defaultconfig.yaml")
	configOverrides := v410_00_assets.MustAsset("v4.1.0/config/config-overrides.yaml")
	specialMergeRules := map[string]resourcemerge.MergeFunc{}

	requiredConfigMap, _, err := resourcemerge.MergePrunedConfigMap(
		&kubecontrolplanev1.KubeAPIServerConfig{},
		configMap,
		"config.yaml",
		specialMergeRules,
		defaultConfig,
		configOverrides,
		operatorSpec.ObservedConfig.Raw,
		operatorSpec.UnsupportedConfigOverrides.Raw,
	)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func managePod(client coreclientv1.ConfigMapsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec, imagePullSpec, operatorImagePullSpec string) (*corev1.ConfigMap, bool, error) {
	appliedPodTemplate, err := manageTemplate(string(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/pod.yaml")), imagePullSpec, operatorImagePullSpec, operatorSpec)
	if err != nil {
		return nil, false, err
	}
	required := resourceread.ReadPodV1OrDie([]byte(appliedPodTemplate))

	var observedConfig map[string]interface{}
	if err := yaml.Unmarshal(operatorSpec.ObservedConfig.Raw, &observedConfig); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal the observedConfig: %v", err)
	}
	proxyConfig, _, err := unstructured.NestedStringMap(observedConfig, "targetconfigcontroller", "proxy")
	if err != nil {
		return nil, false, fmt.Errorf("couldn't get the proxy config from observedConfig: %v", err)
	}

	proxyEnvVars := proxyMapToEnvVars(proxyConfig)
	for i, container := range required.Spec.Containers {
		required.Spec.Containers[i].Env = append(container.Env, proxyEnvVars...)
	}

	configMap := resourceread.ReadConfigMapV1OrDie(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	configMap.Data["forceRedeploymentReason"] = operatorSpec.ForceRedeploymentReason
	configMap.Data["version"] = version.Get().String()
	return resourceapply.ApplyConfigMap(client, recorder, configMap)
}

func manageRolloutMonitorPod(client coreclientv1.ConfigMapsGetter, recorder events.Recorder, operatorImagePullSpec string) (*corev1.ConfigMap, bool, error) {
	required := resourceread.ReadPodV1OrDie(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/rollout-monitor-pod.yaml"))
	required.Spec.Containers[0].Image = operatorImagePullSpec
	required.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways

	configMap := resourceread.ReadConfigMapV1OrDie(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/rollout-monitor-pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	return resourceapply.ApplyConfigMap(client, recorder, configMap)
}

func ManageClientCABundle(lister corev1listers.ConfigMapLister, client coreclientv1.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "client-ca"},
		lister,
		// this is from the installer and contains the value to verify the admin.kubeconfig user
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "admin-kubeconfig-client-ca"},
		// this is from the installer and contains the value to verify the node bootstrapping cert that is baked into images
		// this is from kube-controller-manager and indicates the ca-bundle.crt to verify their signatures (kubelet client certs)
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "csr-controller-ca"},
		// this is from the installer and contains the value to verify the kube-apiserver communicating to the kubelet
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "kube-apiserver-to-kubelet-client-ca"},
		// this bundle is what this operator uses to mint new client certs it directly manages
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "kube-control-plane-signer-ca"},
		// this bundle is what a user uses to mint new client certs it directly manages
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "user-client-ca"},
		// this bundle is what validates the master kubelet bootstrap credential.  Users can invalid this by removing it.
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "kubelet-bootstrap-kubeconfig"},
		// this bundle is what validates kubeconfigs placed on masters
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "node-system-admin-ca"},
	)
	if err != nil {
		return nil, false, err
	}

	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func manageKubeAPIServerCABundle(lister corev1listers.ConfigMapLister, client coreclientv1.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "kube-apiserver-server-ca"},
		lister,
		// this bundle is what this operator uses to mint loadbalancers certs
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "loadbalancer-serving-ca"},
		// this bundle is what this operator uses to mint localhost certs
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "localhost-serving-ca"},
		// this bundle is what a user uses to mint service-network certs
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "service-network-serving-ca"},
		// this bundle is what this operator uses to mint localhost-recovery certs
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "localhost-recovery-serving-ca"},
	)
	if err != nil {
		return nil, false, err
	}

	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func ensureKubeAPIServerTrustedCA(ctx context.Context, client coreclientv1.CoreV1Interface, recorder events.Recorder) error {
	required := resourceread.ReadConfigMapV1OrDie(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/trusted-ca-cm.yaml"))
	cmCLient := client.ConfigMaps(operatorclient.TargetNamespace)

	cm, err := cmCLient.Get(ctx, "trusted-ca-bundle", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = cmCLient.Create(ctx, required, metav1.CreateOptions{})
		}
		return err
	}

	// update if modified by the user
	if val, ok := cm.Labels["config.openshift.io/inject-trusted-cabundle"]; !ok || val != "true" {
		cm.Labels["config.openshift.io/inject-trusted-cabundle"] = "true"
		_, err = cmCLient.Update(ctx, cm, metav1.UpdateOptions{})
		return err
	}

	return err
}

func ensureLocalhostRecoverySAToken(ctx context.Context, client coreclientv1.CoreV1Interface, recorder events.Recorder) error {
	requiredSA := resourceread.ReadServiceAccountV1OrDie(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/localhost-recovery-sa.yaml"))
	requiredToken := resourceread.ReadSecretV1OrDie(v410_00_assets.MustAsset("v4.1.0/kube-apiserver/localhost-recovery-token.yaml"))

	saClient := client.ServiceAccounts(operatorclient.TargetNamespace)
	serviceAccount, err := saClient.Get(ctx, requiredSA.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// The default token secrets get random names so we have created a custom secret
	// to be populated with SA token so we have a stable name.
	secretsClient := client.Secrets(operatorclient.TargetNamespace)
	token, err := secretsClient.Get(ctx, requiredToken.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Token creation / injection for a SA is asynchronous.
	// We will report and error if it's missing, go degraded and get re-queued when the SA token is updated.

	uid := token.Annotations[corev1.ServiceAccountUIDKey]
	if len(uid) == 0 {
		return fmt.Errorf("secret %s/%s hasn't been populated with SA token yet: missing SA UID", token.Namespace, token.Name)
	}

	if uid != string(serviceAccount.UID) {
		return fmt.Errorf("secret %s/%s hasn't been populated with current SA token yet: SA UID mismatch", token.Namespace, token.Name)
	}

	if len(token.Data) == 0 {
		return fmt.Errorf("secret %s/%s hasn't been populated with any data yet", token.Namespace, token.Name)
	}

	// Explicitly check that the fields we use are there, so we find out easily if some are removed or renamed.

	_, ok := token.Data["token"]
	if !ok {
		return fmt.Errorf("secret %s/%s hasn't been populated with current SA token yet", token.Namespace, token.Name)
	}

	_, ok = token.Data["ca.crt"]
	if !ok {
		return fmt.Errorf("secret %s/%s hasn't been populated with current SA token root CA yet", token.Namespace, token.Name)
	}

	return err
}

func proxyMapToEnvVars(proxyConfig map[string]string) []corev1.EnvVar {
	if proxyConfig == nil {
		return nil
	}

	envVars := []corev1.EnvVar{}
	for k, v := range proxyConfig {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// need to sort the slice so that kube-apiserver-pod configmap does not change all the time
	sort.Slice(envVars, func(i, j int) bool { return envVars[i].Name < envVars[j].Name })
	return envVars
}

func gracefulTerminationDurationFromConfig(operatorSpec *operatorv1.StaticPodOperatorSpec) (int, error) {
	var gracefulTerminationDurationPath = []string{"gracefulTerminationDuration"}

	mergedConfigs, err := resourcemerge.MergeProcessConfig(map[string]resourcemerge.MergeFunc{}, operatorSpec.ObservedConfig.Raw, operatorSpec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return 0, err
	}

	// read the watch termination from the observed configuration
	observedConfig := map[string]interface{}{}
	if err := json.NewDecoder(bytes.NewBuffer(mergedConfigs)).Decode(&observedConfig); err != nil {
		return 0, err
	}
	observedGracefulTerminationDurationStr, _, err := unstructured.NestedString(observedConfig, gracefulTerminationDurationPath...)
	if err != nil {
		return 0, fmt.Errorf("unable to extract gracefulTerminationDuration from the observed config: %v, path = %v", err, gracefulTerminationDurationPath)
	}
	if len(observedGracefulTerminationDurationStr) == 0 {
		return 0, nil
	}
	observedGracefulTerminationDuration, err := strconv.Atoi(observedGracefulTerminationDurationStr)
	if err != nil {
		return 0, fmt.Errorf("incorrect value of watchTerminationDuration field in the observed config: %v", err)
	}

	return observedGracefulTerminationDuration, nil
}

type kasTemplate struct {
	Image                       string
	OperatorImage               string
	Verbosity                   string
	GracefulTerminationDuration int
}

func manageTemplate(rawTemplate string, imagePullSpec string, operatorImagePullSpec string, operatorSpec *operatorv1.StaticPodOperatorSpec) (string, error) {
	var verbosity string
	switch operatorSpec.LogLevel {
	case operatorv1.Normal:
		verbosity = fmt.Sprintf(" -v=%d", 2)
	case operatorv1.Debug:
		verbosity = fmt.Sprintf(" -v=%d", 4)
	case operatorv1.Trace:
		verbosity = fmt.Sprintf(" -v=%d", 6)
	case operatorv1.TraceAll:
		verbosity = fmt.Sprintf(" -v=%d", 8)
	default:
		verbosity = fmt.Sprintf(" -v=%d", 2)
	}

	gracefulTerminationDuration, err := gracefulTerminationDurationFromConfig(operatorSpec)
	if err != nil {
		return "", err
	}

	if gracefulTerminationDuration == 0 {
		// apply a default value
		// 135s is our default value
		//   the initial 70s is reserved fo the minimal termination period
		//   additional 60s for finishing all in-flight requests
		//   an extra 5s to make sure the potential SIGTERM will be sent after the server terminates itself
		gracefulTerminationDuration = 135
	}

	tmplVal := kasTemplate{
		Image:                       imagePullSpec,
		OperatorImage:               operatorImagePullSpec,
		Verbosity:                   verbosity,
		GracefulTerminationDuration: gracefulTerminationDuration,
	}
	tmpl, err := template.New("kas").Parse(rawTemplate)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, tmplVal)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
