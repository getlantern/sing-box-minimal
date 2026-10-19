package route

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

type stubPlatformInterface struct {
	adapter.PlatformInterface
}

func (stubPlatformInterface) UsePlatformConnectionOwnerFinder() bool { return true }

func TestProcessSearcherLazyInitCloseRace(t *testing.T) {
	r := &Router{
		logger:            log.NewNOPFactory().NewLogger("test"),
		platformInterface: stubPlatformInterface{},
		needFindProcess:   true,
	}
	r.updateProcessNeeded()
	require.True(t, r.processNeeded.Load(), "expected processNeeded to be true with needFindProcess set")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.updateProcessNeeded()
			r.processInfoSearcher()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = r.Close()
	}()
	wg.Wait()

	require.NotNil(t, r.processInfoSearcher(), "expected a searcher to be initialized after lazy init")
}
