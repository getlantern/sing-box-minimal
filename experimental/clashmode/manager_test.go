package clashmode

import (
	"context"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

type stubDNSRouter struct {
	adapter.DNSRouter
}

func (stubDNSRouter) ClearCache() {}

func TestManagerModeConcurrentWithSetMode(t *testing.T) {
	t.Parallel()
	ctx := service.ContextWith[adapter.DNSRouter](context.Background(), stubDNSRouter{})
	manager := NewManager(ctx, log.NewNOPFactory().Logger(), "a", []string{"a", "b"})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 1000 {
			if i%2 == 0 {
				manager.SetMode("b")
			} else {
				manager.SetMode("a")
			}
		}
	}()
	for range 1000 {
		require.Contains(t, []string{"a", "b"}, manager.Mode())
	}
	wg.Wait()
}
