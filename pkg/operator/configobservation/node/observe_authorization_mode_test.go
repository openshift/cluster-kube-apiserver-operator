package node

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAddAuthorizationModes(t *testing.T) {
	for _, on := range []bool{false, true} {
		expectedSet := []any{"Scope", "SystemMasters", "RBAC", "Node"}
		if on {
			expectedSet = []any{"Scope", "SystemMasters", "RBAC", ModeMinimumKubeletVersion, "Node"}
		}
		for _, tc := range []struct {
			name           string
			existingConfig map[string]interface{}
			expectedConfig map[string]interface{}
		}{
			{
				name: "should not fail if apiServerArguments not present",
				existingConfig: map[string]interface{}{
					"fakeconfig": "fake",
				},
				expectedConfig: map[string]interface{}{
					"fakeconfig":         "fake",
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
			{
				name: "should not fail if authorization-mode not present",
				existingConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"fake": []any{"fake"}},
				},
				expectedConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"fake": []any{"fake"}, "authorization-mode": expectedSet},
				},
			},
			{
				name: "should clobber value if not expected",
				existingConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": []any{"fake"}},
				},
				expectedConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
			{
				name: "should not fail if MinimumKubeletVersion already present",
				existingConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": []any{"MinimumKubeletVersion"}},
				},
				expectedConfig: map[string]interface{}{
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
			{
				name: "should not fail if apiServerArguments not present",
				existingConfig: map[string]interface{}{
					"fakeconfig": "fake",
				},
				expectedConfig: map[string]interface{}{
					"fakeconfig":         "fake",
					"apiServerArguments": map[string]any{"authorization-mode": expectedSet},
				},
			},
		} {
			name := tc.name + " when feature is "
			if on {
				name += "on"
			} else {
				name += "off"
			}
			t.Run(name, func(t *testing.T) {
				if err := AddAuthorizationModes(tc.existingConfig, on); err != nil {
					t.Fatal(err)
				}

				if diff := cmp.Diff(tc.expectedConfig, tc.existingConfig); diff != "" {
					t.Errorf("unexpected config:\n%s", diff)
				}
			})
		}
	}
}
