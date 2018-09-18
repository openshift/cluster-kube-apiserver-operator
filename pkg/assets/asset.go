package assets

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/golang/glog"
)

// TODO: The code below should move to library-go

type Asset struct {
	Name string
	Data []byte
}

type Assets []Asset

func (as Assets) WriteFiles(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	for _, asset := range as {
		if _, err := os.Stat(path); os.IsExist(err) {
			glog.Warningf("File %s already exists and will be replaced", path)
		}
		if err := asset.WriteFile(path); err != nil {
			return err
		}
	}
	return nil
}

func (a Asset) WriteFile(path string) error {
	f := filepath.Join(path, a.Name)
	if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
		return err
	}
	fmt.Printf("Writing asset: %s\n", f)
	return ioutil.WriteFile(f, a.Data, 0600)
}

func MustCreateAssetFromTemplate(name string, template []byte, data interface{}) Asset {
	a, err := assetFromTemplate(name, template, data)
	if err != nil {
		panic(err)
	}
	return a
}

var templateFuncs = map[string]interface{}{
	"base64": base64encode,
	"indent": indent,
}

func indent(indention int, v []byte) string {
	newline := "\n" + strings.Repeat(" ", indention)
	return strings.Replace(string(v), "\n", newline, -1)
}

func base64encode(v []byte) string {
	return base64.StdEncoding.EncodeToString(v)
}

func assetFromTemplate(name string, tb []byte, data interface{}) (Asset, error) {
	tmpl, err := template.New(name).Funcs(templateFuncs).Parse(string(tb))
	if err != nil {
		return Asset{}, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return Asset{}, err
	}
	return Asset{Name: name, Data: buf.Bytes()}, nil
}
