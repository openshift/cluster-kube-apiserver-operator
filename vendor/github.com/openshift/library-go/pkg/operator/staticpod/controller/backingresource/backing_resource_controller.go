package backingresource

import (
	"embed"

	"github.com/openshift/library-go/pkg/assets"
)

//go:embed manifests
var fs embed.FS

func StaticPodManifests(targetNamespace string) func(name string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		config := struct {
			TargetNamespace string
		}{
			TargetNamespace: targetNamespace,
		}
		template, err := fs.ReadFile(name)
		if err != nil {
			panic("unable to read template file " + name + ": " + err.Error())
		}
		return assets.MustCreateAssetFromTemplate(name, template, config).Data, nil
	}
}
