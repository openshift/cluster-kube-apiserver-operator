package startupmonitor

import "time"

// withProbeTimeout specifies a timeout after which the monitor starts the fall back procedure
func (m *monitor) withProbeTimeout(timeout time.Duration) *monitor {
	m.timeout = timeout
	return m
}

// withProbeInterval probeInterval specifies a time interval at which health of the target will be assessed.
// Be mindful of not setting it too low, on each iteration, an i/o is involved
func (m *monitor) withProbeInterval(probeInterval time.Duration) *monitor {
	m.probeInterval = probeInterval
	return m
}

// withTargetName specifies the name of the operand
// used to construct the final file name when reading the current and previous manifests
func (m *monitor) withTargetName(targetName string) *monitor {
	m.targetName = targetName
	return m
}

// withManifestPath points to the directory that holds the root manifests
func (m *monitor) withManifestPath(manifestsPath string) *monitor {
	m.manifestsPath = manifestsPath
	return m
}

// withStaticPodResourcesPath points to the directory that holds all files supporting the static pod manifests
func (m *monitor) withStaticPodResourcesPath(staticPodResourcesPath string) *monitor {
	m.staticPodResourcesPath = staticPodResourcesPath
	return m
}

// withRevision specifies the current revision number
func (m *monitor) withRevision(revision int) *monitor {
	m.revision = revision
	return m
}
