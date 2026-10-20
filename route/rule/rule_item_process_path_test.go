package rule

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/assert"
)

func TestProcessPathItem(t *testing.T) {
	rule := NewProcessPathItem([]string{"/usr/bin/Game"})
	assert.False(t, rule.Match(&adapter.InboundContext{}))
	for _, test := range []struct {
		path  string
		match bool
	}{
		{"", false},
		{"/usr/bin/Game", true},
		{"/usr/bin/game", C.IsWindows},
		{"/usr/bin/Other", false},
		{"/other/bin/Game", false},
		{"/usr/bin/../bin/Game", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			owner := &adapter.ConnectionOwner{ProcessPath: test.path}
			assert.Equal(t, test.match, rule.Match(&adapter.InboundContext{ProcessInfo: owner}))
			assert.Equal(t, test.path, owner.ProcessPath)
		})
	}
}
