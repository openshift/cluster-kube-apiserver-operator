package assets

import (
	"io/ioutil"
	"path/filepath"

	"github.com/golang/glog"
)

var (
	// kubeStaticAssets defines the source template (relative to manifest dir) and the path to output file (relative to asset dir).
	kubeStaticAssets = []string{
		// bootstrap manifests
		"bootstrap-manifests/bootstrap-apiserver.yaml",
		// main namespace
		"manifests/ns.yaml",
		// daemonset manifests
		"manifests/kube-apiserver.yaml",
	}
)

// NewKubernetesStaticAssets processes the manifest templates using provided config and return list of assets that should be written to disk.
func NewKubernetesStaticAssets(manifestTemplateDir string, conf Config) Assets {
	result := Assets{}
	for _, name := range kubeStaticAssets {
		result = append(result, MustCreateAssetFromTemplate(name, mustReadManifest(manifestTemplateDir, name), conf))
	}
	return result
}

func mustReadManifest(manifestTemplateDir string, filename string) []byte {
	manifestFilePath := filepath.Join(manifestTemplateDir, filename)
	out, err := ioutil.ReadFile(manifestFilePath)
	if err != nil {
		glog.Fatalf("Unable to read manifest template %q: %v", manifestFilePath, err)
	}
	return out
}
