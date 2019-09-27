package targetconfigcontroller

import (
	"crypto/x509"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/cert"

	"github.com/openshift/cluster-kube-apiserver-operator/pkg/cmd/regeneratecerts/carry/kubecontrollermanager/operatorclient"
	"github.com/openshift/cluster-kube-apiserver-operator/tls"

	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

var (
	csrSignerName        = tls.OpenShiftKubeAPIServerOperator_CSRSigner.Name
	csrSignerNamespace   = tls.OpenShiftKubeAPIServerOperator_CSRSigner.Namespace
	csrSignerCAName      = tls.OpenShiftKubeAPIServerOperator_CSRSignerCA.Name
	csrSignerCANamespace = tls.OpenShiftKubeAPIServerOperator_CSRSignerCA.Namespace
)

func ManageCSRCABundle(lister corev1listers.ConfigMapLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		tls.OpenShiftKubeAPIServerOperator_CSRControllerCA.ResourceLocation(),
		lister,
		tls.OpenShiftKubeAPIServerOperator_CSRControllerCA.FromResourceLocations()...,
	)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func ManageCSRSigner(lister corev1listers.SecretLister, client corev1client.SecretsGetter, recorder events.Recorder) (*corev1.Secret, bool, error) {
	// get the certkey pair we will sign with. We're going to add the cert to a ca bundle so we can recognize the chain it signs back to the signer
	csrSigner, err := lister.Secrets(operatorclient.OperatorNamespace).Get(csrSignerName)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	// the CSR signing controller only accepts a single cert.  make sure we only ever have one (not multiple to construct a larger chain)
	signingCert := csrSigner.Data["tls.crt"]
	if len(signingCert) == 0 {
		return nil, false, nil
	}
	signingKey := csrSigner.Data["tls.key"]
	if len(signingCert) == 0 {
		return nil, false, nil
	}
	signingCertKeyPair, err := crypto.GetCAFromBytes(signingCert, signingKey)
	if err != nil {
		return nil, false, err
	}
	certBytes, err := crypto.EncodeCertificates(signingCertKeyPair.Config.Certs[0])
	if err != nil {
		return nil, false, err
	}

	csrSigner = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: csrSignerNamespace, Name: csrSignerName},
		Data: map[string][]byte{
			"tls.crt": certBytes,
			"tls.key": []byte(signingKey),
		},
	}
	return resourceapply.ApplySecret(client, recorder, csrSigner)
}

func ManageCSRIntermediateCABundle(lister corev1listers.SecretLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	// get the certkey pair we will sign with. We're going to add the cert to a ca bundle so we can recognize the chain it signs back to the signer
	csrSigner, err := lister.Secrets(csrSignerNamespace).Get(csrSignerName)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	signingCert := csrSigner.Data["tls.crt"]
	if len(signingCert) == 0 {
		return nil, false, nil
	}
	signingKey := csrSigner.Data["tls.key"]
	if len(signingCert) == 0 {
		return nil, false, nil
	}
	signingCertKeyPair, err := crypto.GetCAFromBytes(signingCert, signingKey)
	if err != nil {
		return nil, false, err
	}

	csrSignerCA, err := client.ConfigMaps(csrSignerCANamespace).Get(csrSignerCAName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		csrSignerCA = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: csrSignerCANamespace, Name: csrSignerCAName},
			Data:       map[string]string{},
		}
	} else if err != nil {
		return nil, false, err
	}

	certificates := []*x509.Certificate{}
	caBundle := csrSignerCA.Data["ca-bundle.crt"]
	if len(caBundle) > 0 {
		var err error
		certificates, err = cert.ParseCertsPEM([]byte(caBundle))
		if err != nil {
			return nil, false, err
		}
	}
	certificates = append(certificates, signingCertKeyPair.Config.Certs...)
	certificates = crypto.FilterExpiredCerts(certificates...)

	finalCertificates := []*x509.Certificate{}
	// now check for duplicates. n^2, but super simple
	for i := range certificates {
		found := false
		for j := range finalCertificates {
			if reflect.DeepEqual(certificates[i].Raw, finalCertificates[j].Raw) {
				found = true
				break
			}
		}
		if !found {
			finalCertificates = append(finalCertificates, certificates[i])
		}
	}

	caBytes, err := crypto.EncodeCertificates(finalCertificates...)
	if err != nil {
		return nil, false, err
	}
	csrSignerCA.Data["ca-bundle.crt"] = string(caBytes)

	return resourceapply.ApplyConfigMap(client, recorder, csrSignerCA)
}
