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

	"github.com/openshift/api/annotations"
	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/staticpod/startupmonitor"
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

	isStartupMonitorEnabledFn func() (bool, error)
}

func NewTargetConfigController(
	targetImagePullSpec, operatorImagePullSpec string,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForOpenshiftKubeAPIServerNamespace informers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	isStartupMonitorEnabledFn func() (bool, error),
	eventRecorder events.Recorder,
) factory.Controller {
	c := &TargetConfigController{
		targetImagePullSpec:       targetImagePullSpec,
		operatorImagePullSpec:     operatorImagePullSpec,
		operatorClient:            operatorClient,
		kubeClient:                kubeClient,
		configMapLister:           kubeInformersForNamespaces.ConfigMapLister(),
		isStartupMonitorEnabledFn: isStartupMonitorEnabledFn,
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

	_, _, err := manageKubeAPIServerConfig(ctx, c.kubeClient.CoreV1(), recorder, operatorSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/config", err))
	}
	_, _, err = managePods(ctx, c.kubeClient.CoreV1(), c.isStartupMonitorEnabledFn, recorder, operatorSpec, c.targetImagePullSpec, c.operatorImagePullSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-apiserver-pod", err))
	}
	_, _, err = ManageClientCABundle(ctx, c.configMapLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/client-ca", err))
	}
	_, _, err = manageKubeAPIServerCABundle(ctx, c.configMapLister, c.kubeClient.CoreV1(), recorder)
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

	if len(errors) > 0 {
		condition := operatorv1.OperatorCondition{
			Type:    "TargetConfigControllerDegraded",
			Status:  operatorv1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: v1helpers.NewMultiLineAggregate(errors).Error(),
		}
		if _, _, err := v1helpers.UpdateStaticPodStatus(ctx, c.operatorClient, v1helpers.UpdateStaticPodConditionFn(condition)); err != nil {
			return true, err
		}
		return true, nil
	}

	condition := operatorv1.OperatorCondition{
		Type:   "TargetConfigControllerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if _, _, err := v1helpers.UpdateStaticPodStatus(ctx, c.operatorClient, v1helpers.UpdateStaticPodConditionFn(condition)); err != nil {
		return true, err
	}

	return false, nil
}

func manageKubeAPIServerConfig(ctx context.Context, client coreclientv1.ConfigMapsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(bindata.MustAsset("assets/kube-apiserver/cm.yaml"))
	defaultConfig := bindata.MustAsset("assets/config/defaultconfig.yaml")
	configOverrides := bindata.MustAsset("assets/config/config-overrides.yaml")
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
	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}

func managePods(ctx context.Context, client coreclientv1.ConfigMapsGetter, isStartupMonitorEnabledFn func() (bool, error), recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec, imagePullSpec, operatorImagePullSpec string) (*corev1.ConfigMap, bool, error) {
	appliedPodTemplate, err := manageTemplate(string(bindata.MustAsset("assets/kube-apiserver/pod.yaml")), imagePullSpec, operatorImagePullSpec, operatorSpec)
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

	configMap := resourceread.ReadConfigMapV1OrDie(bindata.MustAsset("assets/kube-apiserver/pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	configMap.Data["forceRedeploymentReason"] = operatorSpec.ForceRedeploymentReason
	configMap.Data["version"] = version.Get().String()

	startupMonitorPodKey, optionalStartupMonitor, err := generateOptionalStartupMonitorPod(isStartupMonitorEnabledFn, operatorSpec, operatorImagePullSpec)
	if err != nil {
		return nil, false, fmt.Errorf("failed to apply an optional pod due to %v", err)
	}
	if optionalStartupMonitor != nil {
		configMap.Data[startupMonitorPodKey] = resourceread.WritePodV1OrDie(optionalStartupMonitor)
	}
	return resourceapply.ApplyConfigMap(ctx, client, recorder, configMap)
}

func generateOptionalStartupMonitorPod(isStartupMonitorEnabledFn func() (bool, error), operatorSpec *operatorv1.StaticPodOperatorSpec, operatorImagePullSpec string) (string, *corev1.Pod, error) {
	if enabled, err := isStartupMonitorEnabledFn(); err != nil {
		return "", nil, err
	} else if !enabled {
		return "", nil, nil
	}

	generatedStartupMonitorPodTemplate, err := startupmonitor.GeneratePodTemplate(operatorSpec, []string{"cluster-kube-apiserver-operator", "startup-monitor"}, operatorclient.TargetNamespace, "kube-apiserver", operatorImagePullSpec, "/var/log/kube-apiserver/startup.log")
	if err != nil {
		return "", nil, err
	}
	required := resourceread.ReadPodV1OrDie([]byte(generatedStartupMonitorPodTemplate))
	return "kube-apiserver-startup-monitor-pod.yaml", required, nil
}

func ManageClientCABundle(ctx context.Context, lister corev1listers.ConfigMapLister, client coreclientv1.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "client-ca"},
		lister,
		certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
		},
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
	if requiredConfigMap.Annotations == nil {
		requiredConfigMap.Annotations = map[string]string{}
	}
	requiredConfigMap.Annotations[annotations.OpenShiftComponent] = "kube-apiserver"

	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}

func manageKubeAPIServerCABundle(ctx context.Context, lister corev1listers.ConfigMapLister, client coreclientv1.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "kube-apiserver-server-ca"},
		lister,
		certrotation.AdditionalAnnotations{
			JiraComponent: "kube-apiserver",
		},
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
	if requiredConfigMap.Annotations == nil {
		requiredConfigMap.Annotations = map[string]string{}
	}
	requiredConfigMap.Annotations[annotations.OpenShiftComponent] = "kube-apiserver"

	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}

func ensureKubeAPIServerTrustedCA(ctx context.Context, client coreclientv1.CoreV1Interface, recorder events.Recorder) error {
	required := resourceread.ReadConfigMapV1OrDie(bindata.MustAsset("assets/kube-apiserver/trusted-ca-cm.yaml"))
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
	requiredSA := resourceread.ReadServiceAccountV1OrDie(bindata.MustAsset("assets/kube-apiserver/localhost-recovery-sa.yaml"))
	requiredToken := resourceread.ReadSecretV1OrDie(bindata.MustAsset("assets/kube-apiserver/localhost-recovery-token.yaml"))

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

func gracefulTerminationDurationFromConfig(config map[string]interface{}) (int, error) {
	// 135s is our default value
	//   the initial 70s is reserved fo the minimal termination period
	//   additional 60s for finishing all in-flight requests
	//   an extra 5s to make sure the potential SIGTERM will be sent after the server terminates itself
	const defaultDuration = 135

	var gracefulTerminationDurationPath = []string{"gracefulTerminationDuration"}

	observedGracefulTerminationDurationStr, _, err := unstructured.NestedString(config, gracefulTerminationDurationPath...)
	if err != nil {
		return 0, fmt.Errorf("unable to extract gracefulTerminationDuration from the observed config: %v, path = %v", err, gracefulTerminationDurationPath)
	}
	if len(observedGracefulTerminationDurationStr) == 0 {
		return defaultDuration, nil
	}
	observedGracefulTerminationDuration, err := strconv.Atoi(observedGracefulTerminationDurationStr)
	if err != nil {
		return 0, fmt.Errorf("incorrect value of watchTerminationDuration field in the observed config: %v", err)
	}

	return observedGracefulTerminationDuration, nil
}

func gogcFromConfig(config map[string]interface{}) (int, error) {
	var gcPercentagePath = []string{"garbageCollectionTargetPercentage"}

	gcPercentage, ok, err := unstructured.NestedString(config, gcPercentagePath...)
	if err != nil {
		return 0, fmt.Errorf("unable to extract %q from the observed config: %v", strings.Join(gcPercentagePath, "."), err)
	}
	if !ok {
		return 100, nil
	}

	// We won't pass along arbitrary GOGC values (like "off"). The configured
	// garbageCollectionTargetPercentage must be a percentage.
	gogc, err := strconv.Atoi(gcPercentage)
	if err != nil {
		return 0, fmt.Errorf("failed to parse observed value of %v: %v", strings.Join(gcPercentagePath, "."), err)
	}

	// clamped to [63, 100] to limit surprises
	if gogc > 100 {
		gogc = 100
	}
	if gogc < 63 {
		gogc = 63
	}

	return gogc, nil
}

type kasTemplate struct {
	Image                         string
	OperatorImage                 string
	Verbosity                     string
	GracefulTerminationDuration   int
	SetupContainerTimeoutDuration int
	GOGC                          int
}

func effectiveConfiguration(spec *operatorv1.StaticPodOperatorSpec) (map[string]interface{}, error) {
	encodedMergedConfig, err := resourcemerge.MergeProcessConfig(map[string]resourcemerge.MergeFunc{}, spec.ObservedConfig.Raw, spec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return nil, err
	}

	effectiveConfig := map[string]interface{}{}
	if err := json.NewDecoder(bytes.NewBuffer(encodedMergedConfig)).Decode(&effectiveConfig); err != nil {
		return nil, err
	}

	return effectiveConfig, nil
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

	config, err := effectiveConfiguration(operatorSpec)
	if err != nil {
		return "", err
	}

	gracefulTerminationDuration, err := gracefulTerminationDurationFromConfig(config)
	if err != nil {
		return "", err
	}

	gogc, err := gogcFromConfig(config)
	if err != nil {
		return "", err
	}

	tmplVal := kasTemplate{
		Image:                       imagePullSpec,
		OperatorImage:               operatorImagePullSpec,
		Verbosity:                   verbosity,
		GracefulTerminationDuration: gracefulTerminationDuration,
		// 80s for minimum-termination-duration (10s port wait, 65s to let pending requests finish after port has been freed) + 5s extra cri-o's graceful termination period
		SetupContainerTimeoutDuration: gracefulTerminationDuration + 80 + 5,
		GOGC:                          gogc,
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
