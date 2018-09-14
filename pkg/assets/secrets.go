package assets

import (
	"encoding/base64"
	"io/ioutil"
	"path/filepath"

	"github.com/golang/glog"
)

var (
	secretAssets = []string{
		"kube-apiserver-secret-aggregator-client-cert.yaml",
		"kube-apiserver-secret-etcd-client-cert.yaml",
		"kube-apiserver-secret-kubelet-client-cert.yaml",
		"kube-apiserver-secret-serving-cert.yaml",
	}
)

func LoadLocalSecrets(configDir string) KubeAPIServerSecretsConfig {
	conf := KubeAPIServerSecretsConfig{}

	key, crt := mustReadKeyPairFile(configDir, "openshift-aggregator")
	conf.AggregatorClientCertCrt = crt
	conf.AggregatorClientCertKey = key

	key, crt = mustReadKeyPairFile(configDir, "master.etcd-client")
	conf.EtcdClientCertCrt = crt
	conf.EtcdClientCertKey = key

	key, crt = mustReadKeyPairFile(configDir, "master.kubelet-client")
	conf.KubeletClientCertCrt = crt
	conf.KubeletClientCertKey = key

	key, crt = mustReadKeyPairFile(configDir, "master.server")
	conf.ServingCertCrt = crt
	conf.ServingCertKey = key

	return conf
}

func NewSecretStaticAssets(manifestDir string, conf Config) Assets {
	result := Assets{}
	for _, assetFile := range secretAssets {
		result = append(result, MustCreateAssetFromTemplate(assetFile, mustReadManifest(manifestDir, assetFile), conf.Secrets))
	}
	return result
}

func mustReadKeyPairFile(configDir string, filename string) ([]byte, []byte) {
	keyFilePath := filepath.Join(configDir, filename+".key")
	crtFilePath := filepath.Join(configDir, filename+".crt")
	key, err := ioutil.ReadFile(keyFilePath)
	if err != nil {
		glog.Fatalf("Unable to read required key file %q: %v", keyFilePath, err)
	}
	crt, err := ioutil.ReadFile(crtFilePath)
	if err != nil {
		glog.Fatalf("Unable to read required crt file %q: %v", crtFilePath, err)
	}
	var (
		keyEncoded, crtEncoded []byte
	)
	base64.StdEncoding.Encode(key, keyEncoded)
	base64.StdEncoding.Encode(crt, crtEncoded)
	return keyEncoded, crtEncoded
}
