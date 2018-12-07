// Code generated by go-bindata.
// sources:
// bindata/v3.11.0/kube-apiserver/cm.yaml
// bindata/v3.11.0/kube-apiserver/defaultconfig.yaml
// bindata/v3.11.0/kube-apiserver/installer-pod.yaml
// bindata/v3.11.0/kube-apiserver/ns.yaml
// bindata/v3.11.0/kube-apiserver/operator-config.yaml
// bindata/v3.11.0/kube-apiserver/pod-cm.yaml
// bindata/v3.11.0/kube-apiserver/pod.yaml
// bindata/v3.11.0/kube-apiserver/svc.yaml
// DO NOT EDIT!

package v311_00_assets

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type asset struct {
	bytes []byte
	info  os.FileInfo
}

type bindataFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (fi bindataFileInfo) Name() string {
	return fi.name
}
func (fi bindataFileInfo) Size() int64 {
	return fi.size
}
func (fi bindataFileInfo) Mode() os.FileMode {
	return fi.mode
}
func (fi bindataFileInfo) ModTime() time.Time {
	return fi.modTime
}
func (fi bindataFileInfo) IsDir() bool {
	return false
}
func (fi bindataFileInfo) Sys() interface{} {
	return nil
}

var _v3110KubeApiserverCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-apiserver
  name: config
data:
  config.yaml:
`)

func v3110KubeApiserverCmYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverCmYaml, nil
}

func v3110KubeApiserverCmYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v3110KubeApiserverDefaultconfigYaml = []byte(`apiVersion: kubecontrolplane.config.openshift.io/v1
kind: KubeAPIServerConfig
admissionPluginConfig:
  ExternalIPRanger:
    configuration:
      allowIngressIP: true
      apiVersion: v1
      externalIPNetworkCIDRs: null
      kind: ExternalIPRangerAdmissionConfig
    location: ""
  openshift.io/RestrictedEndpointsAdmission:
    configuration:
      apiVersion: v1
      kind: RestrictedEndpointsAdmissionConfig
      restrictedCIDRs:
      - 10.3.0.0/16 # ServiceCIDR
      - 10.2.0.0/16 # ClusterCIDR
    location: ""
aggregatorConfig:
  proxyClientInfo:
    certFile: /etc/kubernetes/static-pod-resources/secrets/aggregator-client/tls.crt
    keyFile: /etc/kubernetes/static-pod-resources/secrets/aggregator-client/tls.key
apiServerArguments:
  storage-backend:
  - etcd3
  storage-media-type:
  - application/vnd.kubernetes.protobuf
  target-ram-mb:
  - "1024"
auditConfig:
  auditFilePath: ""
  enabled: false
  logFormat: ""
  maximumFileRetentionDays: 0
  maximumFileSizeMegabytes: 0
  maximumRetainedFiles: 0
  policyConfiguration: null
  policyFile: ""
  webHookKubeConfig: ""
  webHookMode: ""
authConfig:
  oauthMetadataFile: ""
  requestHeader:
    clientCA: /etc/kubernetes/static-pod-resources/configmaps/aggregator-client-ca/ca-bundle.crt
    clientCommonNames:
    - kube-apiserver-proxy
    - system:kube-apiserver-proxy
    - system:openshift-aggregator
    extraHeaderPrefixes:
    - X-Remote-Extra-
    groupHeaders:
    - X-Remote-Group
    usernameHeaders:
    - X-Remote-User
  webhookTokenAuthenticators: null
consolePublicURL: ""
corsAllowedOrigins:
- //127\.0\.0\.1(:|$)
- //localhost(:|$)
imagePolicyConfig:
  externalRegistryHostname: ""
  internalRegistryHostname: docker-registry.default.svc:5000
kubeletClientInfo:
  # empty until it's properly secured
  ca: ""
  certFile: /etc/kubernetes/static-pod-resources/secrets/kubelet-client/tls.crt
  keyFile: /etc/kubernetes/static-pod-resources/secrets/kubelet-client/tls.key
  port: 10250
oauthConfig:
  alwaysShowProviderSelection: false
  assetPublicURL: # To be filled
  grantConfig:
    method: auto
    serviceAccountMethod: prompt
  masterCA: /etc/kubernetes/secrets/root-ca.crt
  masterPublicURL: # To be filled
  masterURL: # To be filled
  sessionConfig:
    sessionMaxAgeSeconds: 300
    sessionName: ssn
    sessionSecretsFile: ""
  templates: null
  tokenConfig:
    accessTokenMaxAgeSeconds: 86400
    authorizeTokenMaxAgeSeconds: 300
projectConfig:
  defaultNodeSelector: ""
serviceAccountPublicKeyFiles:
- /etc/kubernetes/static-pod-resources/configmaps/sa-token-signing-certs/ca-bundle.crt
servicesNodePortRange: 30000-32767
servicesSubnet: 10.3.0.0/16 # ServiceCIDR
servingInfo:
  bindAddress: 0.0.0.0:6443
  bindNetwork: tcp4
  certFile: /etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.crt
  clientCA: /etc/kubernetes/static-pod-resources/configmaps/client-ca/ca-bundle.crt
  keyFile: /etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.key
  maxRequestsInFlight: 1200
  namedCertificates: null
  requestTimeoutSeconds: 3600
storageConfig:
  ca: /etc/kubernetes/static-pod-resources/configmaps/etcd-serving-ca/ca-bundle.crt
  certFile: /etc/kubernetes/static-pod-resources/secrets/etcd-client/tls.crt
  keyFile: /etc/kubernetes/static-pod-resources/secrets/etcd-client/tls.key
  storagePrefix: openshift.io
  urls: null
userAgentMatchingConfig:
  defaultRejectionMessage: ""
  deniedClients: null
  requiredClients: null
`)

func v3110KubeApiserverDefaultconfigYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverDefaultconfigYaml, nil
}

func v3110KubeApiserverDefaultconfigYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverDefaultconfigYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/defaultconfig.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v3110KubeApiserverInstallerPodYaml = []byte(`apiVersion: v1
kind: Pod
metadata:
  namespace: openshift-kube-apiserver
  name: installer-<deployment-id>-<nodeName>
  labels:
    app: installer
spec:
  serviceAccountName: installer-sa
  priorityClassName: system-node-critical
  containers:
  - name: apiserver
    image: ${IMAGE}
    imagePullPolicy: Always
    command: ["cluster-kube-apiserver-operator", "installer"]
    securityContext:
      privileged: true
      runAsUser: 0
    terminationMessagePolicy: FallbackToLogsOnError
    volumeMounts:
    - mountPath: /etc/kubernetes/
      name: kubelet-dir
  restartPolicy: Never
  securityContext:
    runAsUser: 0
  volumes:
  - hostPath:
      path: /etc/kubernetes/
    name: kubelet-dir
`)

func v3110KubeApiserverInstallerPodYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverInstallerPodYaml, nil
}

func v3110KubeApiserverInstallerPodYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverInstallerPodYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/installer-pod.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v3110KubeApiserverNsYaml = []byte(`apiVersion: v1
kind: Namespace
metadata:
  name: openshift-kube-apiserver
  labels:
    openshift.io/run-level: "0"
`)

func v3110KubeApiserverNsYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverNsYaml, nil
}

func v3110KubeApiserverNsYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverNsYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/ns.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v3110KubeApiserverOperatorConfigYaml = []byte(`apiVersion: kubeapiserver.operator.openshift.io/v1alpha1
kind: KubeAPIServerOperatorConfig
metadata:
  name: instance
spec:
  managementState: Managed
`)

func v3110KubeApiserverOperatorConfigYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverOperatorConfigYaml, nil
}

func v3110KubeApiserverOperatorConfigYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverOperatorConfigYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/operator-config.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v3110KubeApiserverPodCmYaml = []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  namespace: openshift-kube-apiserver
  name: kube-apiserver-pod
data:
  pod.yaml:
  forceRedeploymentReason:
  version:
`)

func v3110KubeApiserverPodCmYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverPodCmYaml, nil
}

func v3110KubeApiserverPodCmYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverPodCmYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/pod-cm.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v3110KubeApiserverPodYaml = []byte(`apiVersion: v1
kind: Pod
metadata:
  namespace: openshift-kube-apiserver
  name: openshift-kube-apiserver
  labels:
    app: openshift-kube-apiserver
    apiserver: "true"
    revision: "REVISION"
spec:
  containers:
  - name: openshift-kube-apiserver
    image: ${IMAGE}
    imagePullPolicy: IfNotPresent
    terminationMessagePolicy: FallbackToLogsOnError
    command: ["hypershift", "openshift-kube-apiserver"]
    args:
    - "--config=/etc/kubernetes/static-pod-resources/configmaps/config/config.yaml"
    resources:
      requests:
        memory: 1Gi
    ports:
    - containerPort: 6443
    volumeMounts:
    - mountPath: /etc/kubernetes/static-pod-resources
      name: resource-dir
    livenessProbe:
      httpGet:
        scheme: HTTPS
        port: 6443
        path: healthz
      initialDelaySeconds: 45
      timeoutSeconds: 10
    readinessProbe:
      httpGet:
        scheme: HTTPS
        port: 6443
        path: healthz
      initialDelaySeconds: 10
      timeoutSeconds: 10
  hostNetwork: true
  priorityClassName: system-node-critical
  tolerations:
  - operator: "Exists"
  volumes:
  - hostPath:
      path: /etc/kubernetes/static-pod-resources/kube-apiserver-pod-REVISION
    name: resource-dir
`)

func v3110KubeApiserverPodYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverPodYaml, nil
}

func v3110KubeApiserverPodYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverPodYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/pod.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

var _v3110KubeApiserverSvcYaml = []byte(`apiVersion: v1
kind: Service
metadata:
  namespace: openshift-kube-apiserver
  name: apiserver
  annotations:
    service.alpha.openshift.io/serving-cert-secret-name: serving-cert
    prometheus.io/scrape: "true"
    prometheus.io/scheme: https
spec:
  type: ClusterIP
  selector:
    apiserver: "true"
  ports:
  - name: https
    port: 443
    targetPort: 6443
`)

func v3110KubeApiserverSvcYamlBytes() ([]byte, error) {
	return _v3110KubeApiserverSvcYaml, nil
}

func v3110KubeApiserverSvcYaml() (*asset, error) {
	bytes, err := v3110KubeApiserverSvcYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "v3.11.0/kube-apiserver/svc.yaml", size: 0, mode: os.FileMode(0), modTime: time.Unix(0, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

// Asset loads and returns the asset for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func Asset(name string) ([]byte, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("Asset %s can't read by error: %v", name, err)
		}
		return a.bytes, nil
	}
	return nil, fmt.Errorf("Asset %s not found", name)
}

// MustAsset is like Asset but panics when Asset would return an error.
// It simplifies safe initialization of global variables.
func MustAsset(name string) []byte {
	a, err := Asset(name)
	if err != nil {
		panic("asset: Asset(" + name + "): " + err.Error())
	}

	return a
}

// AssetInfo loads and returns the asset info for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func AssetInfo(name string) (os.FileInfo, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("AssetInfo %s can't read by error: %v", name, err)
		}
		return a.info, nil
	}
	return nil, fmt.Errorf("AssetInfo %s not found", name)
}

// AssetNames returns the names of the assets.
func AssetNames() []string {
	names := make([]string, 0, len(_bindata))
	for name := range _bindata {
		names = append(names, name)
	}
	return names
}

// _bindata is a table, holding each asset generator, mapped to its name.
var _bindata = map[string]func() (*asset, error){
	"v3.11.0/kube-apiserver/cm.yaml":              v3110KubeApiserverCmYaml,
	"v3.11.0/kube-apiserver/defaultconfig.yaml":   v3110KubeApiserverDefaultconfigYaml,
	"v3.11.0/kube-apiserver/installer-pod.yaml":   v3110KubeApiserverInstallerPodYaml,
	"v3.11.0/kube-apiserver/ns.yaml":              v3110KubeApiserverNsYaml,
	"v3.11.0/kube-apiserver/operator-config.yaml": v3110KubeApiserverOperatorConfigYaml,
	"v3.11.0/kube-apiserver/pod-cm.yaml":          v3110KubeApiserverPodCmYaml,
	"v3.11.0/kube-apiserver/pod.yaml":             v3110KubeApiserverPodYaml,
	"v3.11.0/kube-apiserver/svc.yaml":             v3110KubeApiserverSvcYaml,
}

// AssetDir returns the file names below a certain
// directory embedded in the file by go-bindata.
// For example if you run go-bindata on data/... and data contains the
// following hierarchy:
//     data/
//       foo.txt
//       img/
//         a.png
//         b.png
// then AssetDir("data") would return []string{"foo.txt", "img"}
// AssetDir("data/img") would return []string{"a.png", "b.png"}
// AssetDir("foo.txt") and AssetDir("notexist") would return an error
// AssetDir("") will return []string{"data"}.
func AssetDir(name string) ([]string, error) {
	node := _bintree
	if len(name) != 0 {
		cannonicalName := strings.Replace(name, "\\", "/", -1)
		pathList := strings.Split(cannonicalName, "/")
		for _, p := range pathList {
			node = node.Children[p]
			if node == nil {
				return nil, fmt.Errorf("Asset %s not found", name)
			}
		}
	}
	if node.Func != nil {
		return nil, fmt.Errorf("Asset %s not found", name)
	}
	rv := make([]string, 0, len(node.Children))
	for childName := range node.Children {
		rv = append(rv, childName)
	}
	return rv, nil
}

type bintree struct {
	Func     func() (*asset, error)
	Children map[string]*bintree
}

var _bintree = &bintree{nil, map[string]*bintree{
	"v3.11.0": {nil, map[string]*bintree{
		"kube-apiserver": {nil, map[string]*bintree{
			"cm.yaml":              {v3110KubeApiserverCmYaml, map[string]*bintree{}},
			"defaultconfig.yaml":   {v3110KubeApiserverDefaultconfigYaml, map[string]*bintree{}},
			"installer-pod.yaml":   {v3110KubeApiserverInstallerPodYaml, map[string]*bintree{}},
			"ns.yaml":              {v3110KubeApiserverNsYaml, map[string]*bintree{}},
			"operator-config.yaml": {v3110KubeApiserverOperatorConfigYaml, map[string]*bintree{}},
			"pod-cm.yaml":          {v3110KubeApiserverPodCmYaml, map[string]*bintree{}},
			"pod.yaml":             {v3110KubeApiserverPodYaml, map[string]*bintree{}},
			"svc.yaml":             {v3110KubeApiserverSvcYaml, map[string]*bintree{}},
		}},
	}},
}}

// RestoreAsset restores an asset under the given directory
func RestoreAsset(dir, name string) error {
	data, err := Asset(name)
	if err != nil {
		return err
	}
	info, err := AssetInfo(name)
	if err != nil {
		return err
	}
	err = os.MkdirAll(_filePath(dir, filepath.Dir(name)), os.FileMode(0755))
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(_filePath(dir, name), data, info.Mode())
	if err != nil {
		return err
	}
	err = os.Chtimes(_filePath(dir, name), info.ModTime(), info.ModTime())
	if err != nil {
		return err
	}
	return nil
}

// RestoreAssets restores an asset under the given directory recursively
func RestoreAssets(dir, name string) error {
	children, err := AssetDir(name)
	// File
	if err != nil {
		return RestoreAsset(dir, name)
	}
	// Dir
	for _, child := range children {
		err = RestoreAssets(dir, filepath.Join(name, child))
		if err != nil {
			return err
		}
	}
	return nil
}

func _filePath(dir, name string) string {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	return filepath.Join(append([]string{dir}, strings.Split(cannonicalName, "/")...)...)
}
