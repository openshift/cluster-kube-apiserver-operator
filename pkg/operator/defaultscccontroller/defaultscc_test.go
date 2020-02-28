package defaultscccontroller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRender(t *testing.T) {
	sccsGot, errGot := NewDefaultSCCCache()

	require.NoError(t, errGot)
	require.NotNil(t, sccsGot)

	defaultSCCWant := []string{
		"anyuid",
		"hostaccess",
		"hostmount-anyuid",
		"hostnetwork",
		"nonroot",
		"privileged",
		"restricted",
	}

	require.Equal(t, defaultSCCWant, sccsGot.DefaultSCCNames())

	for _, name := range sccsGot.DefaultSCCNames() {
		sccGot, exists := sccsGot.Get(name)
		require.True(t, exists)
		require.NotNil(t, sccGot)
		require.Equal(t, name, sccGot.GetName())
	}
}
