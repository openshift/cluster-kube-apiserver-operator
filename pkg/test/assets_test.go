package test

import (
	"testing"

	"github.com/openshift/cluster-kube-apiserver-operator/cmd/cluster-kube-apiserver-operator/render"
	"github.com/openshift/library-go/pkg/assets"
	yaml "gopkg.in/yaml.v2"
)

func TestYamlCorrectness(t *testing.T) {
	readAllYaml("../../manifests/", t)
	readAllYaml("../../bindata/", t)
}

func readAllYaml(path string, t *testing.T) error {
	manifests, err := assets.New(path, render.Config{}, assets.OnlyYaml)
	if err != nil {
		return err
	}
	t.Logf("Found %d manifests in %s", len(manifests), path)
	for _, m := range manifests {
		contents := make(map[string]interface{})
		t.Logf("Checking %s...", m.Name)
		if err := yaml.Unmarshal(m.Data, &contents); err != nil {
			t.Errorf("Got unexpected error unmarshaling %s: %v", m.Name, err)
		}
	}
	return nil
}
