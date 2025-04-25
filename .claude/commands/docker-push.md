Run `make images`, then retag the most recent Docker image with $ARGUMENTS and push it to the registry.

Steps:
1. Execute `make images` command
2. Find the most recently created Docker image
3. Tag it with: $ARGUMENTS
4. Push the newly tagged image to the registry
5. Set if `overrides` is not set already in `version` of kind `clusterversion`:
  ```
  spec:
    overrides:
    - group: apps
      kind: Deployment
      name: kube-apiserver-operator
      namespace: openshift-kube-apiserver-operator
      unmanaged: true
  ```
6. Set ONLY the env `OPERATOR_IMAGE` value to the newly pushed image in the `deployment` of the `kube-apiserver-operator` in the `openshift-kube-apiserver-operator` namespace. Do not set any other envs. Not even the `OPERATOR_IMAGE_VERSION` env.
