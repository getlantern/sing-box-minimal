package rule

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func TestRuleSetUpdaterRetriesUntilFirstLoad(t *testing.T) {
	setTimeout(t, &ruleSetRetryMin, 20*time.Millisecond)
	setTimeout(t, &ruleSetRetryMax, 50*time.Millisecond)
	var requests atomic.Int32
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request, done <-chan struct{}) {
		if requests.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(testRuleSetSource))
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ruleSet := newTestRemoteRuleSet(ctx, server.URL)
	ruleSet.httpClient = newTestHTTPClient()
	ruleSet.IncRef()
	updater := NewRuleSetUpdater(ctx, []adapter.RuleSet{ruleSet})
	updater.Start()
	t.Cleanup(func() { updater.Close() })

	require.Eventually(t, func() bool {
		return len(ruleSet.rulesSnapshot()) > 0
	}, 5*time.Second, 10*time.Millisecond, "a never-loaded rule-set must retry well before its 24h update interval")
	require.GreaterOrEqual(t, requests.Load(), int32(3))
}

func TestNextRetryDelay(t *testing.T) {
	require.Equal(t, ruleSetRetryMin, nextRetryDelay(0, 24*time.Hour))
	require.Equal(t, 2*ruleSetRetryMin, nextRetryDelay(ruleSetRetryMin, 24*time.Hour))
	require.Equal(t, ruleSetRetryMax, nextRetryDelay(ruleSetRetryMax, 24*time.Hour))
	require.Equal(t, time.Minute, nextRetryDelay(ruleSetRetryMax, time.Minute), "never waits longer than the update interval")
}
