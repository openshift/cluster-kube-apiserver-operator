package encoding

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiserverconfigv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

var (
	scheme         = runtime.NewScheme()
	codecs         = serializer.NewCodecFactory(scheme)
	jsonSerializer runtime.Serializer
)

func init() {
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(apiserverconfigv1.AddToScheme(scheme))
	info, ok := runtime.SerializerInfoForMediaType(codecs.SupportedMediaTypes(), runtime.ContentTypeJSON)
	if !ok {
		panic("json is not a supported media type")
	}
	jsonSerializer = info.Serializer
}

// EncodeEncryptionConfiguration serializes an EncryptionConfiguration to its serialized representation.
func EncodeEncryptionConfiguration(encryptionConfiguration *apiserverconfigv1.EncryptionConfiguration) ([]byte, error) {
	if encryptionConfiguration == nil {
		return nil, fmt.Errorf("EncryptionConfiguration object cannot be nil")
	}
	encoder := codecs.EncoderForVersion(jsonSerializer, apiserverconfigv1.SchemeGroupVersion)
	encryptionConfigurationData, err := runtime.Encode(encoder, encryptionConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to encode EncryptionConfiguration: %w", err)
	}
	return encryptionConfigurationData, nil
}

// DecodeEncryptionConfiguration extracts an EncryptionConfiguration object from its serialized representation.
func DecodeEncryptionConfiguration(data []byte) (*apiserverconfigv1.EncryptionConfiguration, error) {
	encryptionConfiguration := &apiserverconfigv1.EncryptionConfiguration{}
	err := runtime.DecodeInto(codecs.UniversalDecoder(apiserverconfigv1.SchemeGroupVersion), data, encryptionConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to decode EncryptionConfiguration: %w", err)
	}
	return encryptionConfiguration, nil
}

// EncodeKMSConfiguration serializes a KMSConfiguration into an EncryptionConfiguration wrapper.
// We use an EncryptionConfiguration as an envelope type because KMSConfiguration is not a runtime.Object.
func EncodeKMSConfiguration(encryption *apiserverconfigv1.KMSConfiguration) ([]byte, error) {
	if encryption == nil {
		return nil, fmt.Errorf("KMSConfiguration object cannot be nil")
	}
	encryptionConfiguration := &apiserverconfigv1.EncryptionConfiguration{
		Resources: []apiserverconfigv1.ResourceConfiguration{
			{
				Providers: []apiserverconfigv1.ProviderConfiguration{
					{KMS: encryption},
				},
			},
		},
	}
	return EncodeEncryptionConfiguration(encryptionConfiguration)
}

// DecodeKMSConfiguration extracts a KMSConfiguration from its serialized EncryptionConfiguration wrapper.
// We use an EncryptionConfiguration as an envelope type because KMSConfiguration is not a runtime.Object.
func DecodeKMSConfiguration(data []byte) (*apiserverconfigv1.KMSConfiguration, error) {
	encryptionConfiguration, err := DecodeEncryptionConfiguration(data)
	if err != nil {
		return nil, err
	}
	// This should never happen, unless the object was not serialized with EncodeKMSConfiguration
	if len(encryptionConfiguration.Resources) != 1 || len(encryptionConfiguration.Resources[0].Providers) != 1 {
		return nil, fmt.Errorf("invalid KMS encryption config")
	}
	return encryptionConfiguration.Resources[0].Providers[0].KMS, nil
}

// EncodeKMSPluginConfig serializes a configv1.KMSPluginConfig into a configv1.APIServer wrapper.
// We use a configv1.APIServer as an envelope type because configv1.KMSPluginConfig is not a runtime.Object.
func EncodeKMSPluginConfig(kmsConfig configv1.KMSPluginConfig) ([]byte, error) {
	apiServerObj := &configv1.APIServer{
		Spec: configv1.APIServerSpec{
			Encryption: configv1.APIServerEncryption{
				KMS: kmsConfig,
			},
		},
	}
	encoder := codecs.EncoderForVersion(jsonSerializer, configv1.SchemeGroupVersion)
	pluginData, err := runtime.Encode(encoder, apiServerObj)
	if err != nil {
		return nil, fmt.Errorf("failed to encode KMS plugin config: %w", err)
	}
	return pluginData, nil
}

// DecodeKMSPluginConfig extracts a configv1.KMSPluginConfig object from its serialized configv1.APIServer wrapper.
// We use a configv1.APIServer as an envelope type because KMSPluginConfig is not a runtime.Object.
func DecodeKMSPluginConfig(data []byte) (configv1.KMSPluginConfig, error) {
	apiServer := &configv1.APIServer{}
	err := runtime.DecodeInto(codecs.UniversalDecoder(configv1.SchemeGroupVersion), data, apiServer)
	if err != nil {
		return configv1.KMSPluginConfig{}, err
	}
	return apiServer.Spec.Encryption.KMS, nil
}
