package rule

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

func TestProcessPathItemWindowsCache(t *testing.T) {
	for _, test := range []struct {
		name  string
		reset func(*adapter.InboundContext)
	}{
		{"rule cache", (*adapter.InboundContext).ResetRuleCache},
		{"match cache", (*adapter.InboundContext).ResetRuleMatchCache},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, short := windowsProcessPathFixture(t)
			other := filepath.Join(filepath.Dir(path), "Other.exe")
			matching := NewProcessPathItem([]string{path})
			nonmatching := NewProcessPathItem([]string{other})
			owner := &adapter.ConnectionOwner{ProcessPath: short}
			metadata := &adapter.InboundContext{ProcessInfo: owner}

			require.False(t, nonmatching.Match(metadata))
			require.NoError(t, os.Remove(path))
			test.reset(metadata)
			require.True(t, matching.Match(metadata), "later rules must reuse the resolved path")
			require.False(t, matching.Match(&adapter.InboundContext{ProcessInfo: owner}), "a new connection must resolve its own path")
			require.Equal(t, short, owner.ProcessPath)

			copied := *metadata
			metadata.ProcessInfo = &adapter.ConnectionOwner{ProcessPath: other}
			require.False(t, matching.Match(metadata), "changing the source path must invalidate the cache")
			require.True(t, matching.Match(&copied), "metadata copies must retain their own cached identity")
		})
	}
}

func TestProcessPathItemWindowsNestedCache(t *testing.T) {
	for _, test := range []struct {
		name        string
		firstResult bool
		build       func(adapter.HeadlessRule, adapter.HeadlessRule) adapter.HeadlessRule
	}{
		{
			name:        "logical and",
			firstResult: true,
			build: func(first, second adapter.HeadlessRule) adapter.HeadlessRule {
				return headlessLogicalRule(C.LogicalTypeAnd, false, first, second)
			},
		},
		{
			name: "logical or",
			build: func(first, second adapter.HeadlessRule) adapter.HeadlessRule {
				return headlessLogicalRule(C.LogicalTypeOr, false, first, second)
			},
		},
		{
			name: "local rule set",
			build: func(first, second adapter.HeadlessRule) adapter.HeadlessRule {
				return newLocalRuleSetForTest("paths", first, second)
			},
		},
		{
			name: "remote rule set",
			build: func(first, second adapter.HeadlessRule) adapter.HeadlessRule {
				return newRemoteRuleSetForTest("paths", first, second)
			},
		},
		{
			name: "rule set item",
			build: func(first, second adapter.HeadlessRule) adapter.HeadlessRule {
				return &RuleSetItem{setList: []adapter.RuleSet{
					newLocalRuleSetForTest("first", first),
					newLocalRuleSetForTest("second", second),
				}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, short := windowsProcessPathFixture(t)
			matching := NewProcessPathItem([]string{path})
			nonmatching := NewProcessPathItem([]string{filepath.Join(filepath.Dir(path), "Other.exe")})
			owner := &adapter.ConnectionOwner{ProcessPath: short}
			metadata := &adapter.InboundContext{ProcessInfo: owner}
			first := processPathCacheRule(func(metadata *adapter.InboundContext) bool {
				require.False(t, nonmatching.Match(metadata))
				require.NoError(t, os.Remove(path))
				return test.firstResult
			})
			secondCalled := false
			second := processPathCacheRule(func(metadata *adapter.InboundContext) bool {
				secondCalled = true
				matched := matching.Match(metadata)
				require.True(t, matched, "sibling rules must reuse the resolved path")
				return matched
			})

			require.True(t, test.build(first, second).Match(metadata))
			require.True(t, secondCalled)
			require.True(t, matching.Match(metadata), "nested rules must preserve the cache for their caller")
			require.Equal(t, short, owner.ProcessPath)
		})
	}
}

type processPathCacheRule func(*adapter.InboundContext) bool

func (r processPathCacheRule) Match(metadata *adapter.InboundContext) bool {
	return r(metadata)
}

func (r processPathCacheRule) String() string {
	return "process-path-cache-test"
}
