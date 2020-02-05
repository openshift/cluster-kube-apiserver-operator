package test

import (
	"os"
	"strings"
	"testing"

	"github.com/ghodss/yaml"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/render"
	"github.com/openshift/library-go/pkg/assets"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestYamlCorrectness(t *testing.T) {
	readAllYaml("../../manifests/", t)
	readAllYaml("../../bindata/", t)
}

func readAllYaml(path string, t *testing.T) {
	excludedManifests := sets.String{}
	excludedManifests.Insert("recovery-pod.yaml")                                                // can't evaluate field KubeApiserver Image in type render.TemplateData
	excludedManifests.Insert("0000_90_kube-apiserver-operator_04_servicemonitor-apiserver.yaml") // fails to parse "$labels" variable https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/#templating

	manifests, err := assets.New(path, render.TemplateData{}, func(info os.FileInfo) bool {
		return assets.OnlyYaml(info) && !excludedManifests.Has(info.Name())
	})
	if err != nil {
		t.Errorf("Unexpected error reading manifests from %s: %v", path, err)
	}
	t.Logf("Found %d manifests in %s", len(manifests), path)
	for _, m := range manifests {
		contents := make(map[string]interface{})
		t.Logf("Checking %s...", m.Name)

		// drop Golang template directive on a complete line
		lines := []string{}
		for _, l := range strings.Split(string(m.Data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "${") && !strings.HasPrefix(strings.TrimSpace(l), "{{") {
				lines = append(lines, l)
			}
		}

		if err := yaml.Unmarshal(m.Data, &contents); err != nil {
			t.Errorf("Unexpected error unmarshaling %s: %v", m.Name, err)
		}
	}
}
