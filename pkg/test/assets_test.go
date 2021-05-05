package test

import (
	"os"
	"strings"
	"testing"

	"github.com/ghodss/yaml"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/render"
	"github.com/openshift/library-go/pkg/assets"
)

func TestYamlCorrectness(t *testing.T) {
	readAllYaml("../../manifests/", t)
	readAllYaml("../../bindata/", t)
}

func readAllYaml(path string, t *testing.T) {
	// TODO: validate also recovery manifest but they take different template and are covered by unit tests
	manifests, err := assets.New(path, render.TemplateData{}, func(info os.FileInfo) bool {
		return assets.OnlyYaml(info) && !strings.HasPrefix(info.Name(), "recovery-") &&
			// the dashboard is a ConfigMap yaml but it has an embedded json in data that causes the reader to fail.
			!strings.HasSuffix(info.Name(), "api_performance_dashboard.yaml") &&
			// there is an alert message containing $labels strings that cause the reader to fail.
			!strings.HasSuffix(info.Name(), "servicemonitor-apiserver.yaml") &&
			!strings.HasSuffix(info.Name(), "etcd-object-counts.yaml")
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
