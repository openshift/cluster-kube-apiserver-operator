package assets

import (
	"io/ioutil"
	"path/filepath"

	"github.com/golang/glog"
)

var (
	configMapAssets = []string{
		"manifests/configmap-kube-apiserver-config.yaml",
		"manifests/configmap-aggregator-client-ca.yaml",
		"manifests/configmap-client-ca.yaml",
		"manifests/configmap-etcd-serving-ca.yaml",
		"manifests/configmap-kubelet-serving-ca.yaml",
		"manifests/configmap-sa-token-signing-certs.yaml",
	}
)

func LoadLocalConfigMaps(configDir string) KubeAPIServerConfigMapsConfig {
	conf := KubeAPIServerConfigMapsConfig{}

	body := mustReadFile(configDir, "master.etcd-client-ca.crt")
	conf.EtcdServingCA = body

	body = mustReadFile(configDir, "ca.crt")
	conf.KubeletServingCA = body
	conf.ClientCA = body

	body = mustReadFile(configDir, "frontproxy-ca.crt")
	conf.AggregatorClientCA = body

	body = mustReadFile(configDir, "serviceaccounts.public.key")
	conf.SATokenSigningCerts = body

	return conf
}

func NewConfigStaticAssets(manifestDir string, conf Config) Assets {
	result := Assets{}
	for _, assetName := range configMapAssets {
		result = append(result, MustCreateAssetFromTemplate(assetName, mustReadManifest(manifestDir, assetName), conf.ConfigMaps))
	}
	return result
}

func mustReadFile(configDir string, filename string) []byte {
	filePath := filepath.Join(configDir, filename)
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		glog.Fatalf("Unable to read required file %q: %v", filePath, err)
	}
	return content
}
