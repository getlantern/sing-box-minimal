package adapter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizedProcessPath(t *testing.T) {
	t.Parallel()
	var metadata InboundContext
	require.Empty(t, metadata.NormalizedProcessPath())
	metadata.ProcessInfo = &ConnectionOwner{}
	require.Empty(t, metadata.NormalizedProcessPath())

	for _, path := range []string{"first.exe", "second.exe", ""} {
		metadata.ProcessInfo.ProcessPath = path
		require.Equal(t, path, metadata.NormalizedProcessPath())
		require.Equal(t, path, metadata.ProcessInfo.ProcessPath)
	}
	metadata.ProcessInfo.ProcessPath = "first.exe"
	require.Equal(t, "first.exe", metadata.NormalizedProcessPath())
	metadata.ProcessInfo = nil
	require.Empty(t, metadata.NormalizedProcessPath())
}

func TestCopyProcessPathCacheFrom(t *testing.T) {
	t.Parallel()
	source := &InboundContext{ProcessInfo: &ConnectionOwner{ProcessPath: "source.exe"}}
	require.Equal(t, "source.exe", source.NormalizedProcessPath())
	for _, test := range []struct {
		name        string
		processInfo *ConnectionOwner
		want        string
	}{
		{"missing process", nil, ""},
		{"empty path", &ConnectionOwner{}, ""},
		{"different path", &ConnectionOwner{ProcessPath: "other.exe"}, ""},
		{"same path", &ConnectionOwner{ProcessPath: "source.exe"}, "source.exe"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata := &InboundContext{ProcessInfo: test.processInfo, DidMatch: true}
			metadata.CopyProcessPathCacheFrom(source)
			require.Equal(t, test.want, metadata.processPathSource)
			require.Equal(t, test.want, metadata.normalizedProcessPath)
			require.True(t, metadata.DidMatch, "copying the path cache must not change match state")
			require.Same(t, test.processInfo, metadata.ProcessInfo)
		})
	}
}
