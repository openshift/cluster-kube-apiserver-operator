package resources

import "time"

// TODO: add this "resources" package to every operator

var (
	AggregatorProxyClientCert = RotatedKeyCertSecret{
		KeyCertSecret: KeyCertSecret{
			Secret: Secret{
				Object: Object{
					Namespace: TargetNamespace,
					Name:      "aggregator-client",
				},
			},
		},
		SigningKey: &KeyCertSecret{
			Secret: Secret{
				Object: Object{
					Namespace: OperatorNamespace,
					Name:      "aggregator-client-signer",
				},
			},
		},
		PublicCABundle: &CABundle{
			ConfigMap: ConfigMap{
				Object: Object{
					Namespace: OperatorNamespace,
					Name:      "managed-aggregator-client-ca",
				},
			},
		},

		CAValidity:          1 * 8 * time.Hour,
		CARefreshPercentage: 0.5,

		CertValidity:          1 * 4 * time.Hour,
		CertRefreshPercentage: 0.5,
	}
)
