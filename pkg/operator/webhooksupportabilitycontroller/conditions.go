package webhooksupportabilitycontroller

const (
	// MutatingAdmissionWebhookConfigurationDegradedType is true when there
	// is a problem with a mutating admission webhook service.
	MutatingAdmissionWebhookConfigurationDegradedType = "MutatingAdmissionWebhookConfigurationError"

	// ValidatingAdmissionWebhookConfigurationDegradedType is true when there
	// is a problem with a validating admission webhook service.
	ValidatingAdmissionWebhookConfigurationDegradedType = "ValidatingAdmissionWebhookConfigurationError"

	// CRDConversionWebhookConfigurationDegradedType is true when there
	// is a problem with a custom resource definition conversion webhook service.
	CRDConversionWebhookConfigurationDegradedType = "CRDConversionWebhookConfigurationError"

	// VirtualResourceAdmissionDegradedType is true when a dynamic admission webhook matches
	// a virtual resource.
	VirtualResourceAdmissionDegradedType = "VirtualResourceAdmissionError"
)

const (
	// WebhookServiceNotFoundReason indicates that a webhook service could not be resolved.
	WebhookServiceNotFoundReason = "WebhookServiceNotFound"

	// WebhookServiceConnectionErrorReason indicates that a connection to a webhook service
	// could not be established.
	WebhookServiceConnectionErrorReason = "WebhookServiceConnectionError"

	// WebhookServiceNotReadyReason indicates that webhook services are having a variety of
	// problems.
	WebhookServiceNotReadyReason = "WebhookServiceNotReady"

	// AdmissionWebhookMatchesVirtualResourceReason indicates that an admission webhook matches
	// a virtual resource.
	AdmissionWebhookMatchesVirtualResourceReason = "AdmissionWebhookMatchesVirtualResource"
)
