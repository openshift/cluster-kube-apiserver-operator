package tlsprofileupgradegatecontroller

import (
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	corev1 "k8s.io/api/core/v1"
)

func TestUpdateGateKeyIfNeeded(t *testing.T) {
	const key = tlsProfileGateKey
	const msg = tlsProfileGateMessage

	tests := []struct {
		name string

		gateNeeded bool
		data       map[string]string
		expected   map[string]string
	}{
		{
			name:       "gate needed, nil data => add key",
			gateNeeded: true,
			data:       nil,
			expected:   map[string]string{key: msg},
		},
		{
			name:       "gate needed, key missing => add key",
			gateNeeded: true,
			data:       map[string]string{"other": "x"},
			expected:   map[string]string{"other": "x", key: msg},
		},
		{
			name:       "gate needed, key present with same message => no change",
			gateNeeded: true,
			data:       map[string]string{key: msg},
			expected:   map[string]string{key: msg},
		},
		{
			name:       "gate needed, key present with stale message => update message",
			gateNeeded: true,
			data:       map[string]string{key: "stale"},
			expected:   map[string]string{key: msg},
		},
		{
			name:       "gate not needed, key present => delete key",
			gateNeeded: false,
			data:       map[string]string{"other": "x", key: msg},
			expected:   map[string]string{"other": "x"},
		},
		{
			name:       "gate not needed, key missing => no change",
			gateNeeded: false,
			data:       map[string]string{"other": "x"},
			expected:   map[string]string{"other": "x"},
		},
		{
			name:       "gate not needed, nil data => no change",
			gateNeeded: false,
			data:       nil,
			expected:   nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{Data: test.data}
			updateGateKeyIfNeeded(test.gateNeeded, cm)
			if !reflect.DeepEqual(test.expected, cm.Data) {
				t.Fatal(spew.Sdump(cm.Data))
			}
		})
	}
}
