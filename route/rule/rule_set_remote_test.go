package rule

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

const testRuleSetSource = `{"version":1,"rules":[{"domain":["example.com"]}]}`

type testHTTPTransport struct {
	*http.Transport
}

func (t testHTTPTransport) Reset() {
	t.CloseIdleConnections()
}

type testHTTPClientManager struct {
	adapter.HTTPClientManager
	transport adapter.HTTPTransport
}

func (m testHTTPClientManager) DefaultTransport() adapter.HTTPTransport {
	return m.transport
}

func newTestHTTPClient() *http.Client {
	return &http.Client{Transport: testHTTPTransport{&http.Transport{}}}
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
	ruleSet, err := NewRemoteRuleSet(ctx, logger.NOP(), "test", option.RuleSet{
		Type:          C.RuleSetTypeRemote,
		Format:        C.RuleSetFormatSource,
		RemoteOptions: option.RemoteRuleSet{URL: url},
	})
	if err != nil {
		panic(err)
	}
	return ruleSet
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
	ruleSet.httpClient = newTestHTTPClient()

	start := time.Now()
	err := ruleSet.fetch(context.Background(), false)
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
	ruleSet.httpClient = newTestHTTPClient()

	start := time.Now()
	require.NoError(t, ruleSet.fetch(context.Background(), false))
	require.Greater(t, time.Since(start), fetchStallTimeout)
	require.Len(t, ruleSet.rules, 1)
}

func TestRemoteRuleSetStartContextBoundsInitialFetch(t *testing.T) {
	setTimeout(t, &initialFetchTimeout, 100*time.Millisecond)
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request, done <-chan struct{}) {
		block(r, done)
	})
	ctx := service.ContextWith[adapter.HTTPClientManager](context.Background(), testHTTPClientManager{transport: testHTTPTransport{&http.Transport{}}})
	ruleSet := newTestRemoteRuleSet(ctx, server.URL)
	t.Cleanup(func() { ruleSet.Close() })
	startContext := adapter.NewHTTPStartContext()
	t.Cleanup(startContext.Close)

	start := time.Now()
	require.NoError(t, ruleSet.StartContext(context.Background(), startContext))
	require.Less(t, time.Since(start), 5*time.Second)
	require.True(t, ruleSet.lastUpdated.IsZero())
}

func runRemoteRuleSetTask(t *testing.T, task func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		task()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("remote rule-set operation deadlocked")
	}
}

func TestRemoteRuleSetConcurrentReload(t *testing.T) {
	t.Parallel()
	s := newTestRemoteRuleSet(context.Background(), "")
	content := []byte(testRuleSetSource)
	require.NoError(t, s.loadBytes(content))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, task := range []func(){
		func() {
			for range 500 {
				if err := s.loadBytes(content); err != nil {
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
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			task()
		}()
	}
	close(start)
	runRemoteRuleSetTask(t, workers.Wait)
	s.IncRef()
	require.NoError(t, s.loadBytes(content))
	s.Cleanup()
	metadata := testMetadata("example.com")
	require.True(t, s.Match(&metadata))
	s.DecRef()
	s.Cleanup()
	require.Empty(t, s.String())
}

func TestRemoteRuleSetConcurrentClose(t *testing.T) {
	t.Parallel()
	s := newTestRemoteRuleSet(context.Background(), "")
	require.NoError(t, s.loadBytes([]byte(testRuleSetSource)))
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for range 500 {
			metadata := testMetadata("example.com")
			s.Match(&metadata)
			_ = s.String()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		require.NoError(t, s.Close())
	}()
	close(start)
	runRemoteRuleSetTask(t, workers.Wait)
}

func TestRemoteRuleSetLoadAfterCloseKeepsRulesCleared(t *testing.T) {
	t.Parallel()
	s := newTestRemoteRuleSet(context.Background(), "")
	content := []byte(testRuleSetSource)
	require.NoError(t, s.loadBytes(content))
	require.NoError(t, s.Close())
	require.NoError(t, s.loadBytes(content))
	metadata := testMetadata("example.com")
	require.False(t, s.Match(&metadata))
	require.Empty(t, s.String())
}
