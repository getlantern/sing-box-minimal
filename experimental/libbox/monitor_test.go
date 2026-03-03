package libbox

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
)

// mockInterfaceFinder implements control.InterfaceFinder for testing.
type mockInterfaceFinder struct {
	byIndexFunc func(index int) (*control.Interface, error)
}

func (f *mockInterfaceFinder) Update() error                                  { return nil }
func (f *mockInterfaceFinder) Interfaces() []control.Interface                { return nil }
func (f *mockInterfaceFinder) ByName(name string) (*control.Interface, error) { return nil, nil }
func (f *mockInterfaceFinder) ByIndex(index int) (*control.Interface, error) {
	return f.byIndexFunc(index)
}
func (f *mockInterfaceFinder) ByAddr(addr netip.Addr) (*control.Interface, error) { return nil, nil }

// mockNetworkManager implements adapter.NetworkManager for testing.
type mockNetworkManager struct {
	finder          control.InterfaceFinder
	updateErr       error
	updateCallCount int
}

func (m *mockNetworkManager) Start(stage adapter.StartStage) error { return nil }
func (m *mockNetworkManager) Close() error                        { return nil }
func (m *mockNetworkManager) InterfaceFinder() control.InterfaceFinder {
	return m.finder
}
func (m *mockNetworkManager) UpdateInterfaces() error {
	m.updateCallCount++
	return m.updateErr
}
func (m *mockNetworkManager) DefaultNetworkInterface() *adapter.NetworkInterface { return nil }
func (m *mockNetworkManager) NetworkInterfaces() []adapter.NetworkInterface      { return nil }
func (m *mockNetworkManager) AutoDetectInterface() bool                          { return false }
func (m *mockNetworkManager) AutoDetectInterfaceFunc() control.Func              { return nil }
func (m *mockNetworkManager) ProtectFunc() control.Func                          { return nil }
func (m *mockNetworkManager) DefaultOptions() adapter.NetworkOptions {
	return adapter.NetworkOptions{}
}
func (m *mockNetworkManager) RegisterAutoRedirectOutputMark(mark uint32) error { return nil }
func (m *mockNetworkManager) AutoRedirectOutputMark() uint32                   { return 0 }
func (m *mockNetworkManager) AutoRedirectOutputMarkFunc() control.Func {
	return func(network, address string, conn syscall.RawConn) error { return nil }
}
func (m *mockNetworkManager) NetworkMonitor() tun.NetworkUpdateMonitor        { return nil }
func (m *mockNetworkManager) InterfaceMonitor() tun.DefaultInterfaceMonitor   { return nil }
func (m *mockNetworkManager) PackageManager() tun.PackageManager              { return nil }
func (m *mockNetworkManager) WIFIState() adapter.WIFIState                    { return adapter.WIFIState{} }
func (m *mockNetworkManager) ResetNetwork()                                   {}
func (m *mockNetworkManager) UpdateWIFIState()                                {}

// mockLogger implements logger.Logger and records which levels were called.
type mockLogger struct {
	mu       sync.Mutex
	debugLog [][]any
	errorLog [][]any
}

func (l *mockLogger) Trace(args ...any) {}
func (l *mockLogger) Debug(args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debugLog = append(l.debugLog, args)
}
func (l *mockLogger) Info(args ...any)  {}
func (l *mockLogger) Warn(args ...any)  {}
func (l *mockLogger) Error(args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errorLog = append(l.errorLog, args)
}
func (l *mockLogger) Fatal(args ...any) {}
func (l *mockLogger) Panic(args ...any) {}

func (l *mockLogger) debugCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.debugLog)
}

func (l *mockLogger) errorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.errorLog)
}

// newTestMonitor creates a platformDefaultInterfaceMonitor wired to mocks.
func newTestMonitor(finder control.InterfaceFinder) (*platformDefaultInterfaceMonitor, *mockNetworkManager, *mockLogger) {
	nm := &mockNetworkManager{finder: finder}
	lg := &mockLogger{}
	wrapper := &platformInterfaceWrapper{
		networkManager: nm,
	}
	mon := &platformDefaultInterfaceMonitor{
		platformInterfaceWrapper: wrapper,
		logger:                   lg,
	}
	return mon, nm, lg
}

// callbackRecorder registers a callback on the monitor and records invocations.
type callbackRecorder struct {
	mu         sync.Mutex
	calls      []callbackCall
}

type callbackCall struct {
	iface *control.Interface
	flags int
}

func (r *callbackRecorder) register(mon *platformDefaultInterfaceMonitor) {
	mon.RegisterCallback(func(defaultInterface *control.Interface, flags int) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, callbackCall{iface: defaultInterface, flags: flags})
	})
}

func (r *callbackRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *callbackRecorder) last() callbackCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

func TestUpdateDefaultInterface_FallbackOnByIndexError(t *testing.T) {
	finder := &mockInterfaceFinder{
		byIndexFunc: func(index int) (*control.Interface, error) {
			return nil, errors.New("no such network interface")
		},
	}
	mon, _, lg := newTestMonitor(finder)
	rec := &callbackRecorder{}
	rec.register(mon)

	mon.updateDefaultInterface("tun0", 42, false, false)

	// The fallback should have constructed an interface from callback params.
	iface := mon.DefaultInterface()
	if iface == nil {
		t.Fatal("expected defaultInterface to be set, got nil")
	}
	if iface.Index != 42 {
		t.Errorf("expected Index 42, got %d", iface.Index)
	}
	if iface.Name != "tun0" {
		t.Errorf("expected Name \"tun0\", got %q", iface.Name)
	}
	if iface.Flags&net.FlagUp == 0 {
		t.Errorf("expected FlagUp to be set, got %v", iface.Flags)
	}

	// Callback should have been invoked with the fallback interface.
	if rec.count() != 1 {
		t.Fatalf("expected 1 callback invocation, got %d", rec.count())
	}
	cbIface := rec.last().iface
	if cbIface == nil || cbIface.Index != 42 || cbIface.Name != "tun0" {
		t.Errorf("callback received unexpected interface: %+v", cbIface)
	}

	// Should log at Debug level, not Error.
	if lg.debugCount() == 0 {
		t.Error("expected Debug log for fallback, got none")
	}
	if lg.errorCount() != 0 {
		t.Errorf("expected no Error logs, got %d", lg.errorCount())
	}
}

func TestUpdateDefaultInterface_NormalPath(t *testing.T) {
	wlan0 := &control.Interface{
		Index: 16,
		Name:  "wlan0",
		MTU:   1500,
		Flags: net.FlagUp | net.FlagMulticast,
	}
	finder := &mockInterfaceFinder{
		byIndexFunc: func(index int) (*control.Interface, error) {
			if index == 16 {
				return wlan0, nil
			}
			return nil, errors.New("not found")
		},
	}
	mon, _, lg := newTestMonitor(finder)
	rec := &callbackRecorder{}
	rec.register(mon)

	mon.updateDefaultInterface("wlan0", 16, true, false)

	iface := mon.DefaultInterface()
	if iface == nil {
		t.Fatal("expected defaultInterface to be set, got nil")
	}
	// Should use the interface returned by ByIndex, not a fallback.
	if iface != wlan0 {
		t.Errorf("expected exact interface from ByIndex, got different pointer")
	}
	if iface.MTU != 1500 {
		t.Errorf("expected MTU 1500 (from ByIndex), got %d", iface.MTU)
	}

	if rec.count() != 1 {
		t.Fatalf("expected 1 callback invocation, got %d", rec.count())
	}
	if rec.last().iface != wlan0 {
		t.Error("callback should receive the ByIndex interface")
	}

	// isExpensive should be propagated.
	if !mon.isExpensive {
		t.Error("expected isExpensive to be true")
	}

	// No error logs on the normal path.
	if lg.errorCount() != 0 {
		t.Errorf("expected no Error logs, got %d", lg.errorCount())
	}
}

func TestUpdateDefaultInterface_IndexMinusOne(t *testing.T) {
	finder := &mockInterfaceFinder{
		byIndexFunc: func(index int) (*control.Interface, error) {
			t.Error("ByIndex should not be called when index is -1")
			return nil, nil
		},
	}
	mon, _, _ := newTestMonitor(finder)

	// Pre-set a default interface to verify it gets cleared.
	mon.defaultInterface = &control.Interface{Index: 16, Name: "wlan0"}

	rec := &callbackRecorder{}
	rec.register(mon)

	mon.updateDefaultInterface("", -1, false, false)

	if mon.DefaultInterface() != nil {
		t.Error("expected defaultInterface to be nil after index -1")
	}

	if rec.count() != 1 {
		t.Fatalf("expected 1 callback invocation, got %d", rec.count())
	}
	if rec.last().iface != nil {
		t.Error("callback should receive nil interface")
	}
}

func TestUpdateDefaultInterface_Deduplication(t *testing.T) {
	wlan0 := &control.Interface{
		Index: 16,
		Name:  "wlan0",
		Flags: net.FlagUp,
	}
	finder := &mockInterfaceFinder{
		byIndexFunc: func(index int) (*control.Interface, error) {
			// Return a new pointer but same Name/Index — should dedup.
			return &control.Interface{Index: 16, Name: "wlan0", Flags: net.FlagUp}, nil
		},
	}
	mon, _, _ := newTestMonitor(finder)

	// Pre-set the same interface.
	mon.defaultInterface = wlan0

	rec := &callbackRecorder{}
	rec.register(mon)

	mon.updateDefaultInterface("wlan0", 16, false, false)

	// Callbacks should NOT fire because the interface didn't change.
	if rec.count() != 0 {
		t.Errorf("expected 0 callback invocations (dedup), got %d", rec.count())
	}
}

func TestUpdateDefaultInterface_UpdateInterfacesError(t *testing.T) {
	wlan0 := &control.Interface{Index: 16, Name: "wlan0", Flags: net.FlagUp}
	finder := &mockInterfaceFinder{
		byIndexFunc: func(index int) (*control.Interface, error) {
			return wlan0, nil
		},
	}
	mon, nm, lg := newTestMonitor(finder)
	nm.updateErr = errors.New("update failed")

	rec := &callbackRecorder{}
	rec.register(mon)

	mon.updateDefaultInterface("wlan0", 16, false, false)

	// UpdateInterfaces error should be logged but not prevent interface update.
	if lg.errorCount() != 1 {
		t.Errorf("expected 1 Error log for UpdateInterfaces failure, got %d", lg.errorCount())
	}
	if mon.DefaultInterface() == nil {
		t.Error("defaultInterface should still be set despite UpdateInterfaces error")
	}
	if rec.count() != 1 {
		t.Errorf("expected callback to still fire, got %d invocations", rec.count())
	}
}

func TestUpdateDefaultInterface_FallbackPreservesFlags(t *testing.T) {
	finder := &mockInterfaceFinder{
		byIndexFunc: func(index int) (*control.Interface, error) {
			return nil, errors.New("no such network interface: tun0")
		},
	}
	mon, _, _ := newTestMonitor(finder)

	mon.updateDefaultInterface("tun0", 100, true, true)

	iface := mon.DefaultInterface()
	if iface == nil {
		t.Fatal("expected fallback interface, got nil")
	}

	// Fallback interface should have exactly FlagUp.
	if iface.Flags != net.FlagUp {
		t.Errorf("expected Flags == FlagUp (%v), got %v", net.FlagUp, iface.Flags)
	}

	// MTU and Addresses should be zero values (we only have name+index from callback).
	if iface.MTU != 0 {
		t.Errorf("expected MTU 0 on fallback interface, got %d", iface.MTU)
	}

	// isExpensive/isConstrained should still be set on the wrapper.
	if !mon.isExpensive {
		t.Error("expected isExpensive to be true")
	}
	if !mon.isConstrained {
		t.Error("expected isConstrained to be true")
	}
}
