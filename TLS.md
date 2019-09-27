# TLS Documentation

## openshift-kube-apiserver-operator/csr-controller-ca

CA bundle ConfigMap containing these CAs:<details><summary>openshift-kube-apiserver-operator/csr-signer-ca</summary><blockquote>

## openshift-kube-apiserver-operator/csr-signer-ca

ConfigMap

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver-operator/csr-controller-signer-ca</summary><blockquote>

## openshift-kube-apiserver-operator/csr-controller-signer-ca

ConfigMap provided by the ???.
</blockquote></summary></details>

## openshift-config-managed/kube-controller-manager-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

## openshift-config-managed/kube-scheduler-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

## openshift-kube-apiserver/client-ca

CA bundle ConfigMap containing these CAs:<details><summary>openshift-config/admin-kubeconfig-client-ca</summary><blockquote>

## openshift-config/admin-kubeconfig-client-ca

ConfigMap provided by the installer.
</blockquote></summary></details>
<details><summary>openshift-config-managed/csr-controller-ca</summary><blockquote>

## openshift-config-managed/csr-controller-ca

ConfigMap provided by the cluster-kube-controller-manager-operator.
</blockquote></summary></details>
<details><summary>openshift-kube-apiserver-operator/kube-apiserver-to-kubelet-client-ca</summary><blockquote>

## openshift-kube-apiserver-operator/kube-apiserver-to-kubelet-client-ca

ConfigMap

</blockquote></summary></details>
<details><summary>openshift-config-managed/kube-controller-manager-client-cert-key</summary><blockquote>

## openshift-kube-apiserver-operator/kube-control-plane-signer-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-config-managed/kube-controller-manager-client-cert-key</summary><blockquote>

## openshift-config-managed/kube-controller-manager-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

</blockquote></summary></details>
<details><summary>openshift-config-managed/kube-scheduler-client-cert-key</summary><blockquote>

## openshift-config-managed/kube-scheduler-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/kube-apiserver-cert-syncer-client-cert-key</summary><blockquote>

## openshift-kube-apiserver/kube-apiserver-cert-syncer-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/user-client-ca</summary><blockquote>

## openshift-kube-apiserver/user-client-ca

ConfigMap provided by the ???.
</blockquote></summary></details>

## openshift-kube-apiserver/kube-apiserver-server-ca

CA bundle ConfigMap containing these CAs:<details><summary>openshift-kube-apiserver/external-loadbalancer-serving-certkey</summary><blockquote>

## openshift-kube-apiserver/loadbalancer-serving-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-kube-apiserver/external-loadbalancer-serving-certkey</summary><blockquote>

## openshift-kube-apiserver/external-loadbalancer-serving-certkey

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver/loadbalancer-serving-ca.
* signer openshift-kube-apiserver/loadbalancer-serving-signer, validity 87600h0m0s, refreshed every 70080h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/localhost-serving-cert-certkey</summary><blockquote>

## openshift-kube-apiserver-operator/localhost-serving-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-kube-apiserver/localhost-serving-cert-certkey</summary><blockquote>

## openshift-kube-apiserver/localhost-serving-cert-certkey

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/localhost-serving-ca.
* signer openshift-kube-apiserver-operator/localhost-serving-signer, validity 87600h0m0s, refreshed every 70080h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/service-network-serving-certkey</summary><blockquote>

## openshift-kube-apiserver-operator/service-network-serving-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-kube-apiserver/service-network-serving-certkey</summary><blockquote>

## openshift-kube-apiserver/service-network-serving-certkey

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/service-network-serving-ca.
* signer openshift-kube-apiserver-operator/service-network-serving-signer, validity 87600h0m0s, refreshed every 70080h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>

## openshift-kube-apiserver/aggregator-client

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-config-managed/kube-apiserver-aggregator-client-ca.
* signer openshift-kube-apiserver-operator/aggregator-client-signer, validity 720h0m0s, refreshed every 360h0m0s.

## openshift-kube-apiserver/kubelet-client

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-apiserver-to-kubelet-client-ca.
* signer openshift-kube-apiserver-operator/kube-apiserver-to-kubelet-signer, validity 8760h0m0s, refreshed every 70080h0m0s.

## openshift-config-managed/kube-apiserver-client-ca

<details><summary>Copied from ConfigMap openshift-kube-apiserver/client-ca</summary><blockquote>

## openshift-kube-apiserver/client-ca

CA bundle ConfigMap containing these CAs:<details><summary>openshift-config/admin-kubeconfig-client-ca</summary><blockquote>

## openshift-config/admin-kubeconfig-client-ca

ConfigMap provided by the installer.
</blockquote></summary></details>
<details><summary>openshift-config-managed/csr-controller-ca</summary><blockquote>

## openshift-config-managed/csr-controller-ca

ConfigMap provided by the cluster-kube-controller-manager-operator.
</blockquote></summary></details>
<details><summary>openshift-kube-apiserver-operator/kube-apiserver-to-kubelet-client-ca</summary><blockquote>

## openshift-kube-apiserver-operator/kube-apiserver-to-kubelet-client-ca

ConfigMap

</blockquote></summary></details>
<details><summary>openshift-config-managed/kube-controller-manager-client-cert-key</summary><blockquote>

## openshift-kube-apiserver-operator/kube-control-plane-signer-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-config-managed/kube-controller-manager-client-cert-key</summary><blockquote>

## openshift-config-managed/kube-controller-manager-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

</blockquote></summary></details>
<details><summary>openshift-config-managed/kube-scheduler-client-cert-key</summary><blockquote>

## openshift-config-managed/kube-scheduler-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/kube-apiserver-cert-syncer-client-cert-key</summary><blockquote>

## openshift-kube-apiserver/kube-apiserver-cert-syncer-client-cert-key

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/kube-control-plane-signer-ca.
* signer openshift-kube-apiserver-operator/kube-control-plane-signer, validity 1440h0m0s, refreshed every 720h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/user-client-ca</summary><blockquote>

## openshift-kube-apiserver/user-client-ca

ConfigMap provided by the ???.
</blockquote></summary></details>

</blockquote></summary></details>

## openshift-config-managed/kubelet-serving-ca

<details><summary>Copied from ConfigMap openshift-kube-apiserver/kubelet-serving-ca</summary><blockquote>

## openshift-kube-apiserver/kubelet-serving-ca

<details><summary>Copied from ConfigMap openshift-config-managed/csr-controller-ca</summary><blockquote>

## openshift-config-managed/csr-controller-ca

ConfigMap provided by the cluster-kube-controller-manager-operator.
</blockquote></summary></details>

</blockquote></summary></details>

## openshift-config-managed/kube-apiserver-server-ca

<details><summary>Copied from ConfigMap openshift-kube-apiserver/kube-apiserver-server-ca</summary><blockquote>

## openshift-kube-apiserver/kube-apiserver-server-ca

CA bundle ConfigMap containing these CAs:<details><summary>openshift-kube-apiserver/external-loadbalancer-serving-certkey</summary><blockquote>

## openshift-kube-apiserver/loadbalancer-serving-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-kube-apiserver/external-loadbalancer-serving-certkey</summary><blockquote>

## openshift-kube-apiserver/external-loadbalancer-serving-certkey

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver/loadbalancer-serving-ca.
* signer openshift-kube-apiserver/loadbalancer-serving-signer, validity 87600h0m0s, refreshed every 70080h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/localhost-serving-cert-certkey</summary><blockquote>

## openshift-kube-apiserver-operator/localhost-serving-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-kube-apiserver/localhost-serving-cert-certkey</summary><blockquote>

## openshift-kube-apiserver/localhost-serving-cert-certkey

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/localhost-serving-ca.
* signer openshift-kube-apiserver-operator/localhost-serving-signer, validity 87600h0m0s, refreshed every 70080h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>
<details><summary>openshift-kube-apiserver/service-network-serving-certkey</summary><blockquote>

## openshift-kube-apiserver-operator/service-network-serving-ca

Rotation CA bundle updated from key rotation controller for:
<details><summary>openshift-kube-apiserver/service-network-serving-certkey</summary><blockquote>

## openshift-kube-apiserver/service-network-serving-certkey

Rotated key, validity 720h0m0s, refreshed every 360h0m0s.
* CA-bundle openshift-kube-apiserver-operator/service-network-serving-ca.
* signer openshift-kube-apiserver-operator/service-network-serving-signer, validity 87600h0m0s, refreshed every 70080h0m0s.

</blockquote></summary></details>

</blockquote></summary></details>

</blockquote></summary></details>

