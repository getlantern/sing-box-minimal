package route

import (
	"net/netip"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"
)

type stubPlatformInterface struct{}

func (stubPlatformInterface) Initialize(networkManager adapter.NetworkManager) error { return nil }
func (stubPlatformInterface) UsePlatformAutoDetectInterfaceControl() bool            { return false }
func (stubPlatformInterface) AutoDetectInterfaceControl(fd int) error                { return nil }
func (stubPlatformInterface) UsePlatformInterface() bool                             { return false }
func (stubPlatformInterface) OpenInterface(options *tun.Options, platformOptions option.TunPlatformOptions) (tun.Tun, error) {
	return nil, nil
}
func (stubPlatformInterface) UsePlatformDefaultInterfaceMonitor() bool { return false }
func (stubPlatformInterface) CreateDefaultInterfaceMonitor(logger logger.Logger) tun.DefaultInterfaceMonitor {
	return nil
}
func (stubPlatformInterface) UsePlatformNetworkInterfaces() bool                     { return false }
func (stubPlatformInterface) NetworkInterfaces() ([]adapter.NetworkInterface, error) { return nil, nil }
func (stubPlatformInterface) UnderNetworkExtension() bool                            { return false }
func (stubPlatformInterface) NetworkExtensionIncludeAllNetworks() bool               { return false }
func (stubPlatformInterface) ClearDNSCache()                                         {}
func (stubPlatformInterface) RequestPermissionForWIFIState() error                   { return nil }
func (stubPlatformInterface) ReadWIFIState() adapter.WIFIState                       { return adapter.WIFIState{} }
func (stubPlatformInterface) SystemCertificates() []string                           { return nil }
func (stubPlatformInterface) UsePlatformConnectionOwnerFinder() bool                 { return true }
func (stubPlatformInterface) FindConnectionOwner(request *adapter.FindConnectionOwnerRequest) (*adapter.ConnectionOwner, error) {
	return nil, nil
}
func (stubPlatformInterface) UsePlatformWIFIMonitor() bool                              { return false }
func (stubPlatformInterface) UsePlatformNotification() bool                             { return false }
func (stubPlatformInterface) SendNotification(notification *adapter.Notification) error { return nil }
func (stubPlatformInterface) MyInterfaceAddress() []netip.Addr                          { return nil }

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
