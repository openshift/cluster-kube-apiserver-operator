package e2e_encryption_perf

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sync"
	"testing"
	"time"

	g "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-apiserver-operator/pkg/operator/operatorclient"
	operatorencryption "github.com/openshift/cluster-kube-apiserver-operator/test/library/encryption"
	libgotest "github.com/openshift/library-go/test/library"
	library "github.com/openshift/library-go/test/library/encryption"
)

const (
	cmStatsKey      = "created configmaps"
	secretsStatsKey = "created secrets"
)

var providerPerf = flag.String("provider-perf", "aescbc", "encryption provider used by the perf tests")

var _ = g.Describe("[sig-api-machinery] kube-apiserver operator encryption perf", func() {
	g.It("TestPerfEncryption [Serial][Slow][Timeout:60m]", func() {
		TestPerfEncryption(g.GinkgoTB())
	})
})

func TestPerfEncryption(t testing.TB) {
	operatorClient := operatorencryption.GetOperator(t)

	scenario := library.PerfScenario{
		BasicScenario: library.BasicScenario{
			Namespace:                       operatorclient.GlobalMachineSpecifiedConfigNamespace,
			LabelSelector:                   "encryption.apiserver.operator.openshift.io/component" + "=" + operatorclient.TargetNamespace,
			EncryptionConfigSecretName:      fmt.Sprintf("encryption-config-%s", operatorclient.TargetNamespace),
			EncryptionConfigSecretNamespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
			OperatorNamespace:               operatorclient.OperatorNamespace,
			TargetGRs:                       operatorencryption.DefaultTargetGRs,
			AssertFunc:                      operatorencryption.AssertSecretsAndConfigMaps,
		},
		GetOperatorConditionsFunc: func(t testing.TB) ([]operatorv1.OperatorCondition, error) {
			apiServerOperator, err := operatorClient.Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return apiServerOperator.Status.Conditions, nil
		},
		AssertDBPopulatedFunc: func(t testing.TB, errorStore map[string]int, statStore map[string]int) {
			secretsCount, ok := statStore[secretsStatsKey]
			if !ok {
				err := errors.New("missing secrets count stats, can't continue the test")
				require.NoError(t, err)
			}
			if secretsCount < 25000 {
				err := fmt.Errorf("expected to create at least 25000 secrets but %d were created", secretsCount)
				require.NoError(t, err)
			}
			t.Logf("Created %d secrets", secretsCount)

			configMpasCount, ok := statStore[cmStatsKey]
			if !ok {
				err := errors.New("missing configmaps count stats, can't continue the test")
				require.NoError(t, err)
			}
			if configMpasCount < 14000 {
				err := fmt.Errorf("expected to create at least 14000 configmaps but %d were created", configMpasCount)
				require.NoError(t, err)
			}
			t.Logf("Created %d configmaps", configMpasCount)

		},
		AssertMigrationTime: func(t testing.TB, migrationTime time.Duration) {
			t.Logf("migration took %v", migrationTime)
			expectedMigrationTime := 28 * time.Minute
			if migrationTime > expectedMigrationTime {
				t.Errorf("migration took too long (%v), expected it to take no more than %v", migrationTime, expectedMigrationTime)
			}
		},
		DBLoaderWorkers: 3,
		DBLoaderFunc: library.DBLoaderRepeat(1, true,
			createNamespace,
			waitUntilNamespaceActive,
			library.DBLoaderRepeatParallel(5010, 50, false, createConfigMap, reportConfigMap),
			library.DBLoaderRepeatParallel(9010, 50, false, createSecret, reportSecret)),
		EncryptionProvider: configv1.EncryptionType(*providerPerf),
	}

	// Replicate the logic from library.TestPerfEncryption but using testing.TB
	migrationStartedCh := make(chan time.Time, 1)

	populateDatabase(t, scenario.DBLoaderWorkers, scenario.DBLoaderFunc, scenario.AssertDBPopulatedFunc)
	watchForMigrationControllerProgressingConditionAsync(t, scenario.GetOperatorConditionsFunc, migrationStartedCh)
	endTimeStamp := runTestEncryption(t, scenario)

	select {
	case migrationStarted := <-migrationStartedCh:
		scenario.AssertMigrationTime(t, endTimeStamp.Sub(migrationStarted))
	default:
		t.Errorf("unable to calculate the migration time, failed to observe when the migration has started")
	}
}

func runTestEncryption(t testing.TB, scenario library.PerfScenario) time.Time {
	var ts time.Time
	testEncryptionType(t, library.BasicScenario{
		Namespace:                       scenario.Namespace,
		LabelSelector:                   scenario.LabelSelector,
		EncryptionConfigSecretName:      scenario.EncryptionConfigSecretName,
		EncryptionConfigSecretNamespace: scenario.EncryptionConfigSecretNamespace,
		OperatorNamespace:               scenario.OperatorNamespace,
		TargetGRs:                       scenario.TargetGRs,
		AssertFunc: func(t testing.TB, clientSet library.ClientSet, expectedMode configv1.EncryptionType, namespace, labelSelector string) {
			// Note that AssertFunc is executed after an encryption secret has been annotated
			ts = time.Now()
			scenario.AssertFunc(t, clientSet, expectedMode, scenario.Namespace, scenario.LabelSelector)
			t.Logf("AssertFunc for TestEncryption scenario with %q provider took %v", scenario.EncryptionProvider, time.Since(ts))
		},
	}, scenario.EncryptionProvider)
	return ts
}

// testEncryptionType is a helper that replicates library.TestEncryptionType logic using testing.TB
func testEncryptionType(t testing.TB, scenario library.BasicScenario, provider configv1.EncryptionType) {
	switch provider {
	case configv1.EncryptionTypeAESCBC:
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeAESCBC, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeAESCBC, scenario.Namespace, scenario.LabelSelector)
		library.AssertEncryptionConfig(t, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
	case configv1.EncryptionTypeAESGCM:
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeAESGCM, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeAESGCM, scenario.Namespace, scenario.LabelSelector)
		library.AssertEncryptionConfig(t, clientSet, scenario.EncryptionConfigSecretName, scenario.EncryptionConfigSecretNamespace, scenario.TargetGRs)
	case configv1.EncryptionTypeIdentity, "":
		clientSet := library.SetAndWaitForEncryptionType(t, configv1.EncryptionTypeIdentity, scenario.TargetGRs, scenario.Namespace, scenario.LabelSelector)
		scenario.AssertFunc(t, clientSet, configv1.EncryptionTypeIdentity, scenario.Namespace, scenario.LabelSelector)
	default:
		t.Errorf("Unknown encryption type: %s", provider)
		t.FailNow()
	}
}

func createSecret(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "encryption-",
		},
		Data: map[string][]byte{
			"quote": []byte("I have no special talents. I am only passionately curious"),
		},
	}
	_, err := kubeClient.CoreV1().Secrets(namespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	return err
}

func reportSecret(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(secretsStatsKey)
	return nil
}

func createConfigMap(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "encryption-",
		},
		Data:       nil,
		BinaryData: nil,
	}

	_, err := kubeClient.CoreV1().ConfigMaps(namespace).Create(context.TODO(), cm, metav1.CreateOptions{})
	return err
}

func reportConfigMap(_ kubernetes.Interface, _ string, _ func(error), statsCollector func(string)) error {
	statsCollector(cmStatsKey)
	return nil
}

func createNamespace(kubeClient kubernetes.Interface, name string, errorCollector func(error), statsCollector func(string)) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "",
		},
		Status: corev1.NamespaceStatus{},
	}
	_, err := kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	return err
}

func waitUntilNamespaceActive(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string)) error {
	err := wait.Poll(10*time.Millisecond, 30*time.Second, func() (bool, error) {
		ns, err := kubeClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if ns.Status.Phase == corev1.NamespaceActive {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		err = fmt.Errorf("failed waiting for ns to become ready, err %v", err)
		errorCollector(err)
	}
	return err
}

// Helper functions copied from library-go perf_helpers.go (unexported)

func watchForMigrationControllerProgressingConditionAsync(t testing.TB, getOperatorCondFn library.GetOperatorConditionsFuncType, migrationStartedCh chan time.Time) {
	t.Helper()
	go watchForMigrationControllerProgressingCondition(t, getOperatorCondFn, migrationStartedCh)
}

func watchForMigrationControllerProgressingCondition(t testing.TB, getOperatorConditionsFn library.GetOperatorConditionsFuncType, migrationStartedCh chan time.Time) {
	t.Helper()

	waitPollInterval := time.Second
	waitPollTimeout := 10 * time.Minute

	t.Logf("Waiting up to %s for the condition %q with the reason %q to be set to true", waitPollTimeout.String(), "EncryptionMigrationControllerProgressing", "Migrating")
	err := wait.Poll(waitPollInterval, waitPollTimeout, func() (bool, error) {
		conditions, err := getOperatorConditionsFn(t)
		if err != nil {
			return false, err
		}
		for _, cond := range conditions {
			if cond.Type == "EncryptionMigrationControllerProgressing" && cond.Status == operatorv1.ConditionTrue {
				t.Logf("EncryptionMigrationControllerProgressing condition observed at %v", cond.LastTransitionTime)
				migrationStartedCh <- cond.LastTransitionTime.Time
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Logf("failed waiting for the condition %q with the reason %q to be set to true, err was %v", "EncryptionMigrationControllerProgressing", "Migrating", err)
	}
}

func populateDatabase(t testing.TB, workers int, dbLoaderFun library.DBLoaderFuncType, assertDBPopulatedFunc func(t testing.TB, errorStore map[string]int, statStore map[string]int)) {
	t.Helper()
	start := time.Now()
	defer func() {
		end := time.Now()
		t.Logf("Populating etcd took %v", end.Sub(start))
	}()

	r := newRunner()

	// run executes loaderFunc for each worker
	r.run(t, workers, dbLoaderFun)

	assertDBPopulatedFunc(t, r.errorStore, r.statsStore)
}

// runner and related types copied from library-go
type runner struct {
	errorStore map[string]int
	lock       *sync.Mutex

	statsStore map[string]int
	lockStats  *sync.Mutex
	wg         *sync.WaitGroup
}

func newRunner() *runner {
	r := &runner{}

	r.errorStore = map[string]int{}
	r.lock = &sync.Mutex{}
	r.statsStore = map[string]int{}
	r.lockStats = &sync.Mutex{}

	r.wg = &sync.WaitGroup{}

	return r
}

func (r *runner) run(t testing.TB, workers int, workFunc ...library.DBLoaderFuncType) {
	t.Logf("Executing provided load function for %d workers", workers)
	for i := 0; i < workers; i++ {
		wrapper := func(wg *sync.WaitGroup) {
			defer wg.Done()
			kubeClient, err := newKubeClient(300, 600)
			if err != nil {
				t.Errorf("Unable to create a kube client for a worker due to %v", err)
				r.collectError(err)
				return
			}
			_ = runWorkFunctions(kubeClient, "", r.collectError, r.collectStat, workFunc...)
		}
		r.wg.Add(1)
		go wrapper(r.wg)
	}
	r.wg.Wait()
	t.Log("All workers completed successfully")
}

func (r *runner) collectError(err error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	errCount, ok := r.errorStore[err.Error()]
	if !ok {
		r.errorStore[err.Error()] = 1
		return
	}
	errCount += 1
	r.errorStore[err.Error()] = errCount
}

func (r *runner) collectStat(stat string) {
	r.lockStats.Lock()
	defer r.lockStats.Unlock()
	statCount, ok := r.statsStore[stat]
	if !ok {
		r.statsStore[stat] = 1
		return
	}
	statCount += 1
	r.statsStore[stat] = statCount
}

func runWorkFunctions(kubeClient kubernetes.Interface, namespace string, errorCollector func(error), statsCollector func(string), workFunc ...library.DBLoaderFuncType) error {
	if len(namespace) == 0 {
		namespace = createNamespaceName()
	}
	for _, work := range workFunc {
		err := work(kubeClient, namespace, errorCollector, statsCollector)
		if err != nil {
			errorCollector(err)
			return err
		}
	}
	return nil
}

func createNamespaceName() string {
	return fmt.Sprintf("encryption-%s", rand.String(10))
}

func newKubeClient(qps float32, burst int) (kubernetes.Interface, error) {
	kubeConfig, err := libgotest.NewClientConfigForTest()
	if err != nil {
		return nil, err
	}

	kubeConfig.QPS = qps
	kubeConfig.Burst = burst

	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}
	return kubeClient, nil
}
