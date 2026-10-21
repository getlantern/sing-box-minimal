package rule

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestProcessPathItemWindowsAliases(t *testing.T) {
	path, short := windowsProcessPathFixture(t)
	other := filepath.Join(filepath.Dir(path), "Other", filepath.Base(path))
	require.NoError(t, os.Mkdir(filepath.Dir(other), 0o700))
	require.NoError(t, os.WriteFile(other, nil, 0o600))
	for _, configured := range []string{path, short, filepath.ToSlash(strings.ToUpper(path))} {
		t.Run(configured, func(t *testing.T) {
			rule := NewProcessPathItem([]string{configured})
			for _, runtimePath := range []string{path, short, strings.ToUpper(short), filepath.ToSlash(path)} {
				owner := &adapter.ConnectionOwner{ProcessPath: runtimePath}
				assert.True(t, rule.Match(&adapter.InboundContext{ProcessInfo: owner}), runtimePath)
				assert.Equal(t, runtimePath, owner.ProcessPath)
			}
			for _, runtimePath := range []string{other, filepath.Join(filepath.Dir(path), "Launcher.exe")} {
				assert.False(t, rule.Match(&adapter.InboundContext{
					ProcessInfo: &adapter.ConnectionOwner{ProcessPath: runtimePath},
				}), runtimePath)
			}
		})
	}

	regex, err := NewProcessPathRegexItem([]string{"^" + regexp.QuoteMeta(short) + "$"})
	require.NoError(t, err)
	owner := &adapter.ConnectionOwner{ProcessPath: short}
	metadata := &adapter.InboundContext{ProcessInfo: owner}
	assert.True(t, NewProcessPathItem([]string{path}).Match(metadata))
	assert.True(t, regex.Match(metadata))
	assert.Equal(t, short, owner.ProcessPath)
}

func TestProcessPathItemWindowsInvertedRule(t *testing.T) {
	path, short := windowsProcessPathFixture(t)
	for _, invert := range []bool{false, true} {
		rule, err := NewDefaultHeadlessRule(context.Background(), option.DefaultHeadlessRule{
			ProcessPath: []string{path},
			Invert:      invert,
		})
		require.NoError(t, err)
		assert.Equal(t, !invert, rule.Match(&adapter.InboundContext{
			ProcessInfo: &adapter.ConnectionOwner{ProcessPath: short},
		}))
		assert.Equal(t, invert, rule.Match(&adapter.InboundContext{
			ProcessInfo: &adapter.ConnectionOwner{ProcessPath: filepath.Join(filepath.Dir(path), "Other.exe")},
		}))
	}
}

func TestProcessPathItemWindowsUnicodeShortPath(t *testing.T) {
	path, short := windowsProcessPathFixture(t, "İbrahim")
	// Keep the Unicode directory spelling while shortening only the executable.
	runtimePath := filepath.Join(filepath.Dir(path), filepath.Base(short))
	require.NotEqual(t, path, runtimePath)
	owner := &adapter.ConnectionOwner{ProcessPath: runtimePath}
	metadata := &adapter.InboundContext{ProcessInfo: owner}
	assert.True(t, NewProcessPathItem([]string{path}).Match(metadata))
	assert.Equal(t, runtimePath, owner.ProcessPath)
	assert.True(t, NewProcessPathItem([]string{runtimePath}).Match(&adapter.InboundContext{
		ProcessInfo: &adapter.ConnectionOwner{ProcessPath: path},
	}))
}

func windowsProcessPathFixture(t *testing.T, subdirs ...string) (string, string) {
	t.Helper()
	input, err := windows.UTF16PtrFromString(`\\?\` + t.TempDir())
	require.NoError(t, err)
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(input, &buffer[0], uint32(len(buffer)))
	require.NoError(t, err)
	require.Positive(t, n)
	require.Less(t, int(n), len(buffer))
	dir := strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`)
	for _, subdir := range subdirs {
		dir = filepath.Join(dir, subdir)
	}
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "Launcher Child Executable.exe")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	input, err = windows.UTF16PtrFromString(`\\?\` + path)
	require.NoError(t, err)
	n, err = windows.GetShortPathName(input, &buffer[0], uint32(len(buffer)))
	require.NoError(t, err)
	require.Positive(t, n)
	require.Less(t, int(n), len(buffer))
	short := strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`)
	if strings.EqualFold(filepath.Base(short), filepath.Base(path)) {
		if os.Getenv("SING_BOX_REQUIRE_SHORT_PATH_TEST") == "1" {
			t.Fatal("8.3 aliases are disabled on the test volume")
		}
		t.Skip("8.3 aliases are disabled on this volume")
	}
	return path, short
}
