package rule

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/x/list"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func localDomainRules(domain string) []option.HeadlessRule {
	return []option.HeadlessRule{{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{Domain: []string{domain}}}}
}

func runLocalRuleSetTask(t *testing.T, task func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		task()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("local rule-set operation deadlocked")
	}
}

func TestLocalRuleSetConcurrentReload(t *testing.T) {
	s := &LocalRuleSet{ctx: context.Background()}
	rules := localDomainRules("example.com")
	require.NoError(t, s.reloadRules(rules))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, task := range []func(){
		func() {
			for range 500 {
				if err := s.reloadRules(rules); err != nil {
					t.Error(err)
					return
				}
			}
		},
		func() {
			for range 500 {
				metadata := testMetadata("example.com")
				s.Match(&metadata)
				_ = s.String()
				s.Metadata()
				s.ExtractIPSet()
			}
		},
		func() {
			for range 500 {
				s.Cleanup()
			}
		},
	} {
		workers.Go(func() {
			<-start
			task()
		})
	}
	close(start)
	runLocalRuleSetTask(t, workers.Wait)
	s.IncRef()
	require.NoError(t, s.reloadRules(rules))
	s.Cleanup()
	metadata := testMetadata("example.com")
	require.True(t, s.Match(&metadata))
	s.DecRef()
	s.Cleanup()
	require.Empty(t, s.String())
}

func TestLocalRuleSetReloadCallbackReentry(t *testing.T) {
	s := &LocalRuleSet{ctx: context.Background()}
	rules := localDomainRules("example.com")
	rules[0].DefaultOptions.IPCIDR = []string{"203.0.113.0/24"}
	var calls int
	var element *list.Element[adapter.RuleSetUpdateCallback]
	element = s.RegisterCallback(func(it adapter.RuleSet) {
		calls++
		metadata := testMetadata("example.com")
		assert.True(t, it.Match(&metadata))
		assert.NotEmpty(t, it.String())
		assert.True(t, it.Metadata().ContainsIPCIDRRule)
		assert.Len(t, it.ExtractIPSet(), 1)
		it.UnregisterCallback(element)
	})
	runLocalRuleSetTask(t, func() { assert.NoError(t, s.reloadRules(rules)) })
	require.Equal(t, 1, calls)
	require.NoError(t, s.reloadRules(rules))
	require.Equal(t, 1, calls)
}

type localReloadRule struct {
	reload func()
}

func (r *localReloadRule) Match(*adapter.InboundContext) bool {
	r.reload()
	return true
}

func (r *localReloadRule) String() string {
	r.reload()
	return "original"
}

func TestLocalRuleSetTraversalAllowsReload(t *testing.T) {
	for _, operation := range []string{"match", "string"} {
		t.Run(operation, func(t *testing.T) {
			s := &LocalRuleSet{ctx: context.Background()}
			s.rules = []adapter.HeadlessRule{&localReloadRule{reload: func() {
				assert.NoError(t, s.reloadRules(localDomainRules("updated.example")))
			}}}
			runLocalRuleSetTask(t, func() {
				if operation == "match" {
					metadata := testMetadata("original.example")
					assert.True(t, s.Match(&metadata))
				} else {
					assert.Equal(t, "original", s.String())
				}
			})
			metadata := testMetadata("updated.example")
			require.True(t, s.Match(&metadata))
			require.Contains(t, s.String(), "updated.example")
		})
	}
}

func TestLocalRuleSetClosePreventsReload(t *testing.T) {
	s := &LocalRuleSet{ctx: context.Background()}
	rules := localDomainRules("example.com")
	require.NoError(t, s.reloadRules(rules))
	calls := 0
	s.RegisterCallback(func(adapter.RuleSet) { calls++ })
	require.NoError(t, s.Close())
	require.NoError(t, s.reloadRules(rules))
	metadata := testMetadata("example.com")
	require.False(t, s.Match(&metadata))
	require.Empty(t, s.String())
	require.Empty(t, s.ExtractIPSet())
	require.Zero(t, calls)
}
