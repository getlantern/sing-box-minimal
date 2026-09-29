package rule

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

const testRuleSetSource = `{"version":1,"rules":[{"domain":["example.com"]}]}`

type testOutbound struct {
	adapter.Outbound
}

func (testOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return N.SystemDialer.DialContext(ctx, network, destination)
}

type testOutboundManager struct {
	adapter.OutboundManager
}

func (testOutboundManager) Default() adapter.Outbound {
	return testOutbound{}
}

func setTimeout(t *testing.T, target *time.Duration, value time.Duration) {
	original := *target
	*target = value
	t.Cleanup(func() { *target = original })
}

func newTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, done <-chan struct{})) *httptest.Server {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, done)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(done) })
	return server
}

func newTestRemoteRuleSet(ctx context.Context, url string) *RemoteRuleSet {
	return NewRemoteRuleSet(ctx, logger.NOP(), option.RuleSet{
		Type:          C.RuleSetTypeRemote,
		Tag:           "test",
		Format:        C.RuleSetFormatSource,
		RemoteOptions: option.RemoteRuleSet{URL: url},
	})
}

func block(r *http.Request, done <-chan struct{}) {
	select {
	case <-r.Context().Done():
	case <-done:
	}
}

func TestRemoteRuleSetFetchStalledBody(t *testing.T) {
	setTimeout(t, &fetchStallTimeout, 100*time.Millisecond)
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request, done <-chan struct{}) {
		w.Write([]byte(testRuleSetSource[:10]))
		w.(http.Flusher).Flush()
		block(r, done)
	})
	ruleSet := newTestRemoteRuleSet(context.Background(), server.URL)
	ruleSet.dialer = N.SystemDialer

	start := time.Now()
	err := ruleSet.fetch(context.Background(), nil)
	require.ErrorIs(t, err, errFetchStalled)
	require.Less(t, time.Since(start), 5*time.Second)
	require.True(t, ruleSet.lastUpdated.IsZero())
}

func TestRemoteRuleSetFetchSlowBodyNotStalled(t *testing.T) {
	setTimeout(t, &fetchStallTimeout, 200*time.Millisecond)
	const chunkDelay = 50 * time.Millisecond
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request, done <-chan struct{}) {
		for i := 0; i < len(testRuleSetSource); i += 5 {
			w.Write([]byte(testRuleSetSource[i:min(i+5, len(testRuleSetSource))]))
			w.(http.Flusher).Flush()
			time.Sleep(chunkDelay)
		}
	})
	ruleSet := newTestRemoteRuleSet(context.Background(), server.URL)
	ruleSet.dialer = N.SystemDialer

	start := time.Now()
	require.NoError(t, ruleSet.fetch(context.Background(), nil))
	require.Greater(t, time.Since(start), fetchStallTimeout)
	require.Len(t, ruleSet.rules, 1)
}

func TestRemoteRuleSetStartContextBoundsInitialFetch(t *testing.T) {
	setTimeout(t, &initialFetchTimeout, 100*time.Millisecond)
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request, done <-chan struct{}) {
		block(r, done)
	})
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), testOutboundManager{})
	ruleSet := newTestRemoteRuleSet(ctx, server.URL)
	t.Cleanup(func() { ruleSet.Close() })
	startContext := adapter.NewHTTPStartContext(ctx)
	t.Cleanup(startContext.Close)

	start := time.Now()
	require.NoError(t, ruleSet.StartContext(context.Background(), startContext))
	require.Less(t, time.Since(start), 5*time.Second)
	require.True(t, ruleSet.lastUpdated.IsZero())
}
