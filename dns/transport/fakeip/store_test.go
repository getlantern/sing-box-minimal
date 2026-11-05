package fakeip

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/cachefile"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// TestStoreCursorSurvivesUncleanExit simulates a process killed without
// Store.Close: only the cache file's database is closed, so nothing but what
// Create persisted survives. An address handed out by an earlier process must
// not be reassigned, because applications may still hold it from a cached DNS
// answer.
func TestStoreCursorSurvivesUncleanExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	inet4Range := netip.MustParsePrefix("198.18.0.0/15")

	assigned := make(map[netip.Addr]string)
	// The empty run reads the cursor and exits before allocating, which must
	// not lose it either. The long run uses up two full reservations.
	for run, allocations := range []int{3, 0, 2 * reservedAddressCount, 1} {
		cacheFile := cachefile.New(context.Background(), logger.NOP(), option.CacheFileOptions{
			Enabled:     true,
			Path:        path,
			StoreFakeIP: true,
		})
		require.NoError(t, cacheFile.Start(adapter.StartStateInitialize))
		cacheFile.DB.MaxBatchDelay = time.Microsecond
		ctx := service.ContextWith[adapter.CacheFile](context.Background(), cacheFile)
		store := NewStore(ctx, logger.NOP(), inet4Range, netip.Prefix{})
		require.NoError(t, store.Start())

		for address, domain := range assigned {
			loaded, ok := store.Lookup(address)
			require.True(t, ok, "run %d: lost mapping for %s", run, address)
			require.Equal(t, domain, loaded, "run %d: %s", run, address)
		}
		for i := range allocations {
			domain := fmt.Sprintf("run%d-%d.example", run, i)
			address, err := store.Create(domain, false)
			require.NoError(t, err)
			previous, reused := assigned[address]
			require.False(t, reused, "run %d: %s reassigned from %s to %s", run, address, previous, domain)
			assigned[address] = domain
		}

		require.NoError(t, cacheFile.Close())
	}
}

var errMappingNotSaved = errors.New("mapping not saved")

type failingMappingStorage struct {
	*MemoryStorage
}

func (failingMappingStorage) FakeIPStore(netip.Addr, string) error {
	return errMappingNotSaved
}

func TestStoreCreateFailsWhenMappingNotSaved(t *testing.T) {
	store := NewStore(context.Background(), logger.NOP(), netip.MustParsePrefix("198.18.0.0/15"), netip.Prefix{})
	require.NoError(t, store.Start())
	store.storage = failingMappingStorage{NewMemoryStorage()}

	_, err := store.Create("example.com", false)
	require.ErrorIs(t, err, errMappingNotSaved)
}
