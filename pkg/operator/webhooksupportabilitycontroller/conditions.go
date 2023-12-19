package webhooksupportabilitycontroller

const (
	// MutatingAdmissionWebhookConfigurationErrorType is true when there
	// is a problem with a mutating admission webhook service.
	MutatingAdmissionWebhookConfigurationErrorType = "MutatingAdmissionWebhookConfigurationError"

	// ValidatingAdmissionWebhookConfigurationErrorType is true when there
	// is a problem with a validating admission webhook service.
	ValidatingAdmissionWebhookConfigurationErrorType = "ValidatingAdmissionWebhookConfigurationError"

	// CRDConversionWebhookConfigurationErrorType is true when there
	// is a problem with a custom resource definition conversion webhook service.
	CRDConversionWebhookConfigurationErrorType = "CRDConversionWebhookConfigurationError"

	// VirtualResourceAdmissionErrorType is true when a dynamic admission webhook matches
	// a virtual resource.
	VirtualResourceAdmissionErrorType = "VirtualResourceAdmissionError"
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
