package recovery

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"text/template"
	"time"

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientcmdapiv1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/crypto"

	"github.com/openshift/cluster-kube-apiserver-operator/bindata"
)

const (
	KubeApiserverStaticPodFileName  = "kube-apiserver-pod.yaml"
	RecoveryPodFileName             = "recovery-kube-apiserver-pod.yaml"
	RecoveryCofigFileName           = "config.yaml"
	RecoveryEncryptionCofigFileName = "encryption-config"
	AdminKubeconfigFileName         = "admin.kubeconfig"

	RecoveryPodAsset              = "assets/kube-apiserver/recovery-pod.yaml"
	RecoveryConfigAsset           = "assets/kube-apiserver/recovery-config.yaml"
	RecoveryEncryptionConfigAsset = "assets/kube-apiserver/recovery-encryption-config.yaml"
)

type Apiserver struct {
	PodManifestDir        string
	ResourceDirPath       string
	StaticPodResourcesDir string
	recoveryResourcesDir  string

	kubeApiserverStaticPod *corev1.Pod
	restConfig             *rest.Config
	kubeClientSet          *kubernetes.Clientset
}

func (s *Apiserver) GetRecoveryResourcesDir() string {
	return s.recoveryResourcesDir
}

func (s *Apiserver) GetKubeApiserverStaticPod() *corev1.Pod {
	return s.kubeApiserverStaticPod
}

func (s *Apiserver) KubeApiserverManifestPath() string {
	return filepath.Join(s.PodManifestDir, KubeApiserverStaticPodFileName)
}

func (s *Apiserver) RestConfig() (*rest.Config, error) {
	if s.restConfig == nil {
		return nil, errors.New("no rest config is set yet")
	}

	return s.restConfig, nil
}

func (s *Apiserver) KubeConfig() (*clientcmdapiv1.Config, error) {
	restConfig, err := s.RestConfig()
	if err != nil {
		return nil, err
	}

	return &clientcmdapiv1.Config{
		APIVersion: "v1",
		Clusters: []clientcmdapiv1.NamedCluster{
			{
				Name: "recovery",
				Cluster: clientcmdapiv1.Cluster{
					CertificateAuthority: restConfig.CAFile,
					Server:               restConfig.Host,
				},
			},
		},
		Contexts: []clientcmdapiv1.NamedContext{
			{
				Name: "admin",
				Context: clientcmdapiv1.Context{
					Cluster:  "recovery",
					AuthInfo: "admin",
				},
			},
		},
		CurrentContext: "admin",
		AuthInfos: []clientcmdapiv1.NamedAuthInfo{
			{
				Name: "admin",
				AuthInfo: clientcmdapiv1.AuthInfo{
					ClientCertificateData: restConfig.CertData,
					ClientKeyData:         restConfig.KeyData,
				},
			},
		},
	}, nil
}

func (s *Apiserver) GetKubeClientset() (*kubernetes.Clientset, error) {
	if s.kubeClientSet != nil {
		return s.kubeClientSet, nil
	}

	restConfig, err := s.RestConfig()
	if err != nil {
		return nil, err
	}

	kubeClientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes clientset: %v", err)
	}

	s.kubeClientSet = kubeClientset

	return s.kubeClientSet, nil
}

func (s *Apiserver) recoveryPod() (*corev1.Pod, error) {
	// Create the manifest to run recovery apiserver
	recoveryPodTemplateBytes, err := bindata.Asset(RecoveryPodAsset)
	if err != nil {
		return nil, fmt.Errorf("failed to find internal recovery pod asset %q: %v", RecoveryPodAsset, err)
	}

	// Process the template
	t, err := template.New("recovery-pod-template").Parse(string(recoveryPodTemplateBytes))
	if err != nil {
		return nil, fmt.Errorf("fail to parse internal recovery pod template %q: %v", RecoveryPodAsset, err)
	}

	var kubeApiserverImage string
	for _, container := range s.kubeApiserverStaticPod.Spec.Containers {
		if regexp.MustCompile("^kube-apiserver-[a-zA-Z0-9]+$").MatchString(container.Name) || container.Name == "kube-apiserver" {
			kubeApiserverImage = container.Image
		}
	}
	if len(kubeApiserverImage) == 0 {
		return nil, errors.New("failed to find kube-apiserver image")
	}

	recoveryPodBuffer := bytes.NewBuffer(nil)
	err = t.Execute(recoveryPodBuffer, struct {
		KubeApiserverImage string
		ResourceDir        string
	}{
		KubeApiserverImage: kubeApiserverImage,
		ResourceDir:        s.recoveryResourcesDir,
	})
	if err != nil {
		return nil, fmt.Errorf("fail to execute internal recovery pod template %q: %v", RecoveryPodAsset, err)
	}

	recoveryPodObj, err := runtime.Decode(Codecs.UniversalDecoder(corev1.SchemeGroupVersion), recoveryPodBuffer.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to decode internal recovery pod %q: %v", RecoveryPodAsset, err)
	}

	recoveryPod, ok := recoveryPodObj.(*corev1.Pod)
	if !ok {
		return nil, fmt.Errorf("unsupported type: internal recovery pod is not type *corev1.Pod but %T", recoveryPod)
	}

	return recoveryPod, nil
}

func (s *Apiserver) Create() error {
	kubeApiserverManifestPath := s.KubeApiserverManifestPath()
	var err error
	s.kubeApiserverStaticPod, err = ReadManifestToV1Pod(kubeApiserverManifestPath)
	if err != nil {
		return fmt.Errorf("failed to read kube-apiserver pod manifest at %q: %v", kubeApiserverManifestPath, err)
	}

	s.ResourceDirPath, err = GetVolumeHostPathPath("resource-dir", s.kubeApiserverStaticPod.Spec.Volumes)
	if err != nil {
		return fmt.Errorf("failed to find resource-dir: %v", err)
	}

	s.recoveryResourcesDir = filepath.Join(s.StaticPodResourcesDir, "recovery-kube-apiserver-pod")
	err = os.Mkdir(s.recoveryResourcesDir, 755)
	if err != nil {
		if os.IsExist(err) {
			klog.Errorf("Recovery dir %q already exist. Please use `recovery-apiserver destroy` command or remove the dir manually.", s.recoveryResourcesDir)
		}
		return fmt.Errorf("failed to create recovery dir %q: %v", s.recoveryResourcesDir, err)
	}

	// Copy certs for accessing etcd
	for src, dest := range map[string]string{
		"secrets/etcd-client/tls.key":              "etcd-client.key",
		"secrets/etcd-client/tls.crt":              "etcd-client.crt",
		"configmaps/etcd-serving-ca/ca-bundle.crt": "etcd-serving-ca-bundle.crt",
	} {
		err = copyFile(filepath.Join(s.ResourceDirPath, src), filepath.Join(s.recoveryResourcesDir, dest))
		if err != nil {
			return err
		}
	}

	// We are creating only temporary certificates to start the recovery apiserver.
	// A week seem reasonably high for a debug session, while it is easy to create a new one.
	certValidity := 7 * 24 * time.Hour
	klog.Infof("Recovery apiserver certificates will be valid for %v", certValidity)

	// Create root CA
	rootCaConfig, err := crypto.MakeSelfSignedCAConfigForDuration("localhost", certValidity)
	if err != nil {
		return fmt.Errorf("failed to create root-signer CA: %v", err)
	}

	servingCaCertPath := filepath.Join(s.recoveryResourcesDir, "serving-ca.crt")
	err = rootCaConfig.WriteCertConfigFile(servingCaCertPath, filepath.Join(s.recoveryResourcesDir, "serving-ca.key"))
	if err != nil {
		return fmt.Errorf("failed to write root-signer files: %v", err)
	}

	// Create config for recovery apiserver
	recoveryConfigBytes, err := bindata.Asset(RecoveryConfigAsset)
	if err != nil {
		return fmt.Errorf("fail to find internal recovery config asset %q: %v", RecoveryConfigAsset, err)
	}

	recoveryConfigPath := filepath.Join(s.recoveryResourcesDir, RecoveryCofigFileName)
	err = os.WriteFile(recoveryConfigPath, recoveryConfigBytes, 644)
	if err != nil {
		return fmt.Errorf("failed to write recovery config %q: %v", recoveryConfigPath, err)
	}

	// always create a default (empty) encryption config to simplify the deployment
	// only copy the real one if it exists
	encryptionConfigExists, err := fileExists(filepath.Join(s.ResourceDirPath, "secrets/encryption-config/encryption-config"))
	if err != nil {
		return fmt.Errorf("failed to check if encryption config exists: %v", err)
	}
	if encryptionConfigExists {
		err = copyFile(filepath.Join(s.ResourceDirPath, "secrets/encryption-config/encryption-config"), filepath.Join(s.recoveryResourcesDir, RecoveryEncryptionCofigFileName))
		if err != nil {
			return err
		}
	} else {
		recoveryEncryptionConfigBytes, err := bindata.Asset(RecoveryEncryptionConfigAsset)
		if err != nil {
			return fmt.Errorf("fail to find recovery encryption config asset %q: %v", RecoveryEncryptionConfigAsset, err)
		}

		recoveryEncryptionConfigPath := filepath.Join(s.recoveryResourcesDir, RecoveryEncryptionCofigFileName)
		err = os.WriteFile(recoveryEncryptionConfigPath, recoveryEncryptionConfigBytes, 644)
		if err != nil {
			return fmt.Errorf("failed to write recovery encryption config %q: %v", recoveryEncryptionConfigPath, err)
		}
	}

	recoveryPod, err := s.recoveryPod()
	if err != nil {
		return fmt.Errorf("failed to create recovery pod: %v", err)
	}

	recoveryPodBytes, err := yaml.Marshal(recoveryPod)
	if err != nil {
		return fmt.Errorf("failed to marshal recovery pod: %v", err)
	}

	recoveryPodManifestPath := filepath.Join(s.PodManifestDir, RecoveryPodFileName)
	err = os.WriteFile(recoveryPodManifestPath, recoveryPodBytes, 644)
	if err != nil {
		return fmt.Errorf("failed to write recovery pod manifest %q: %v", recoveryPodManifestPath, err)
	}

	// Create client cert
	ca := crypto.CA{
		Config:          rootCaConfig,
		SerialGenerator: &crypto.RandomSerialGenerator{},
	}

	// Create client certificates for system:admin
	// (Reuse the serving CA as client CA, this is fine for shortlived localhost recovery apiserver.)
	clientCert, err := ca.MakeClientCertificateForDuration(
		&user.DefaultInfo{
			Name: "system:admin",
			Groups: []string{
				"system:masters",
			},
		},
		certValidity,
	)
	if err != nil {
		return fmt.Errorf("failed to create client certificate: %v", err)
	}

	clientCertBytes, clientKeyBytes, err := clientCert.GetPEMBytes()

	s.restConfig = &rest.Config{
		Host: "https://localhost:7443",
		TLSClientConfig: rest.TLSClientConfig{
			CAFile:   servingCaCertPath,
			CertData: clientCertBytes,
			KeyData:  clientKeyBytes,
		},
	}

	kubeconfig, err := s.KubeConfig()
	if err != nil {
		return fmt.Errorf("failed to create kubeconfig: %v", err)
	}

	kubeconfigBytes, err := yaml.Marshal(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to marshal kubeconfig: %v", err)
	}

	kubeconfigPath := filepath.Join(s.recoveryResourcesDir, AdminKubeconfigFileName)
	err = os.WriteFile(kubeconfigPath, kubeconfigBytes, 600)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig %q: %v", kubeconfigPath, err)
	}

	return nil
}

func (s *Apiserver) Destroy() error {
	recoveryPodManifestPath := filepath.Join(s.PodManifestDir, RecoveryPodFileName)

	recoveryPod, err := ReadManifestToV1Pod(recoveryPodManifestPath)
	if err != nil {
		return fmt.Errorf("failed to decode file %q: %v", recoveryPodManifestPath, err)
	}

	resourceDirPath, err := GetVolumeHostPathPath("resource-dir", recoveryPod.Spec.Volumes)
	if err != nil {
		return fmt.Errorf("failed to find resource-dir volume for pod manifest %q: %v", recoveryPodManifestPath, err)
	}

	klog.Infof("Deleting resource-dir %q", resourceDirPath)
	err = os.RemoveAll(resourceDirPath)
	if err != nil {
		return fmt.Errorf("failed to remove recovery pod manifest %q: %v", recoveryPodManifestPath, err)
	}

	klog.Infof("Deleting recovery pod manifest %q", recoveryPodManifestPath)
	err = os.Remove(recoveryPodManifestPath)
	if err != nil {
		return fmt.Errorf("failed to remove recovery pod manifest %q: %v", recoveryPodManifestPath, err)
	}

	return nil
}

func copyFile(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open file %q: %v", src, err)
	}
	defer srcFile.Close()

	srcFileInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file %q: %v", src, err)
	}

	if srcFileInfo.IsDir() {
		return fmt.Errorf("can't copy file %q because it is a directory", src)
	}

	destFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, srcFileInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("failed to open file %q: %v", dest, err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy file %q into %q: %v", src, dest, err)
	}

	return nil
}

func fileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		return !info.IsDir(), nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}

	return false, err
}
