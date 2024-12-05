package bindata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"

	kyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func UnstructuredDefaultConfig() (map[string]any, error) {
	asset := filepath.Join("assets", "config", "defaultconfig.yaml")
	raw, err := Asset(asset)
	if err != nil {
		return nil, fmt.Errorf("failed to get default config asset asset=%s - %s", asset, err)
	}

	rawJSON, err := kyaml.ToJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to convert asset yaml to JSON asset=%s - %s", asset, err)
	}

	return ConvertToUnstructured(rawJSON)
}

func ConvertToUnstructured(raw []byte) (map[string]interface{}, error) {
	decoder := json.NewDecoder(bytes.NewBuffer(raw))
	u := map[string]interface{}{}
	if err := decoder.Decode(&u); err != nil {
		return nil, err
	}

	return u, nil
}
