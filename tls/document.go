package tls

var DocumentedResources []interface{}

func (b CombinedCABundle) Document() CombinedCABundle {
	DocumentedResources = append(DocumentedResources, b)
	return b
}

func (s InputSecret) Document() InputSecret {
	DocumentedResources = append(DocumentedResources, s)
	return s
}

func (c InputConfigMap) Document() InputConfigMap {
	DocumentedResources = append(DocumentedResources, c)
	return c
}

func (c RotatedCertificate) Document() RotatedCertificate {
	DocumentedResources = append(DocumentedResources, c)
	return c
}

func (s RotatedSigner) Document() RotatedSigner {
	DocumentedResources = append(DocumentedResources, s)
	return s
}

func (s RotatedCABundle) Document() RotatedCABundle {
	DocumentedResources = append(DocumentedResources, s)
	return s
}

func (s SyncedSecret) Document() SyncedSecret {
	DocumentedResources = append(DocumentedResources, s)
	return s
}

func (c SyncedConfigMap) Document() SyncedConfigMap {
	DocumentedResources = append(DocumentedResources, c)
	return c
}
