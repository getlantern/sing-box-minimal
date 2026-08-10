package outbound

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

type stubOutbound struct {
	tag  string
	deps []string
}

func (s *stubOutbound) Type() string           { return "stub" }
func (s *stubOutbound) Tag() string            { return s.tag }
func (s *stubOutbound) Network() []string      { return nil }
func (s *stubOutbound) Dependencies() []string { return s.deps }
func (s *stubOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, nil
}
func (s *stubOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

// stubRegistry hands back a preconfigured outbound from CreateOutbound; every
// other OutboundRegistry method panics via the embedded nil interface.
type stubRegistry struct {
	adapter.OutboundRegistry
	out adapter.Outbound
}

func (r *stubRegistry) CreateOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) (adapter.Outbound, error) {
	return r.out, nil
}

// Close() used to nil the outbounds slice while leaving outboundByTag
// populated, so a Remove racing shutdown hit panic("invalid inbound index").
// Close must now clear both, and Remove must report the tag as gone.
func TestRemoveAfterClose(t *testing.T) {
	m := NewManager(logger.NOP(), nil, nil, "")
	out := &stubOutbound{tag: "test-out"}
	m.outbounds = append(m.outbounds, out)
	m.outboundByTag[out.Tag()] = out
	m.started = true

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(m.outboundByTag) != 0 {
		t.Fatal("Close must clear outboundByTag along with the slice")
	}
	if err := m.Remove("test-out"); err == nil {
		t.Fatal("Remove after Close should report the tag as gone")
	}
}

// Defense in depth: even if the map and slice somehow desync again (tag in
// outboundByTag with no slice entry), Remove must heal the stale map entry
// instead of panicking.
func TestRemoveHealsDesync(t *testing.T) {
	m := NewManager(logger.NOP(), nil, nil, "")
	out := &stubOutbound{tag: "test-out"}
	m.outboundByTag[out.Tag()] = out

	if err := m.Remove("test-out"); err != nil {
		t.Fatalf("Remove on desynced manager: %v", err)
	}
	if _, found := m.outboundByTag["test-out"]; found {
		t.Fatal("stale outboundByTag entry not cleaned up")
	}
}

// Replacing an outbound via Create must drop the old outbound's dependByTag
// entries; otherwise a stale "is depended by" record blocks removing the
// dependency later.
func TestCreateReplaceCleansDependByTag(t *testing.T) {
	reg := &stubRegistry{}
	m := NewManager(logger.NOP(), reg, nil, "")
	parent := &stubOutbound{tag: "parent"}
	child := &stubOutbound{tag: "child", deps: []string{"parent"}}
	m.outbounds = append(m.outbounds, parent, child)
	m.outboundByTag["parent"] = parent
	m.outboundByTag["child"] = child
	m.dependByTag["parent"] = []string{"child"}

	reg.out = &stubOutbound{tag: "child"} // replacement drops the dependency
	if err := m.Create(context.Background(), nil, nil, "child", "stub", nil); err != nil {
		t.Fatalf("Create replace: %v", err)
	}
	if err := m.Remove("parent"); err != nil {
		t.Fatalf("Remove(parent) blocked by stale dependBy entry: %v", err)
	}
}

func TestRemove(t *testing.T) {
	m := NewManager(logger.NOP(), nil, nil, "")
	out := &stubOutbound{tag: "test-out"}
	m.outbounds = append(m.outbounds, out)
	m.outboundByTag[out.Tag()] = out

	if err := m.Remove("test-out"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(m.outbounds) != 0 {
		t.Fatal("outbound not removed from slice")
	}
	if _, found := m.outboundByTag["test-out"]; found {
		t.Fatal("outbound not removed from map")
	}
}

// lifecycleOutbound is a stubOutbound that participates in the start-stage
// lifecycle, so a test can fail a chosen stage and observe whether Create
// cleaned up after itself.
type lifecycleOutbound struct {
	stubOutbound
	failStage  *adapter.StartStage // stage to fail at, nil to always succeed
	closeErr   error
	started    []adapter.StartStage
	closeCalls int
}

func (l *lifecycleOutbound) Start(stage adapter.StartStage) error {
	if l.failStage != nil && stage == *l.failStage {
		return errors.New("stage failed")
	}
	l.started = append(l.started, stage)
	return nil
}

func (l *lifecycleOutbound) Close() error {
	l.closeCalls++
	return l.closeErr
}

// A replacement that fails partway through its start stages never reaches
// m.outbounds or outboundByTag, so nothing else can ever close it. Create must
// release the stages that did come up, or every failed start leaks whatever the
// outbound allocated (for unbounded, a whole broflake/WebRTC stack).
func TestCreateClosesOutboundOnStartFailure(t *testing.T) {
	t.Parallel()

	failAt := adapter.StartStateStarted // last stage, so the earlier three ran
	out := &lifecycleOutbound{stubOutbound: stubOutbound{tag: "flaky"}, failStage: &failAt}

	m := NewManager(logger.NOP(), &stubRegistry{out: out}, nil, "")
	m.started = true

	err := m.Create(context.Background(), nil, nil, "flaky", "stub", nil)
	if err == nil {
		t.Fatal("Create should surface the start-stage failure")
	}
	if out.closeCalls != 1 {
		t.Fatalf("partially started outbound was not closed (closeCalls=%d); its resources leak "+
			"because it is never registered and so can never be closed later", out.closeCalls)
	}
	if _, found := m.outboundByTag["flaky"]; found {
		t.Fatal("an outbound that failed to start must not be registered")
	}
	if len(m.outbounds) != 0 {
		t.Fatalf("an outbound that failed to start must not be in the active list (len=%d)", len(m.outbounds))
	}
}

// Create used to abort when closing the predecessor returned an error, which
// left the manager inconsistent: the predecessor stayed registered despite
// having been closed, while the replacement — already fully started — was never
// registered and so could never be closed. The swap has to complete.
func TestCreateCompletesSwapWhenPredecessorCloseFails(t *testing.T) {
	t.Parallel()

	old := &lifecycleOutbound{stubOutbound: stubOutbound{tag: "dup"}, closeErr: errors.New("close failed")}
	replacement := &lifecycleOutbound{stubOutbound: stubOutbound{tag: "dup"}}

	m := NewManager(logger.NOP(), &stubRegistry{out: replacement}, nil, "")
	m.started = true
	m.outbounds = append(m.outbounds, old)
	m.outboundByTag["dup"] = old

	if err := m.Create(context.Background(), nil, nil, "dup", "stub", nil); err != nil {
		t.Fatalf("Create must not fail because the predecessor's Close did: %v", err)
	}
	if old.closeCalls != 1 {
		t.Fatalf("predecessor not closed (closeCalls=%d)", old.closeCalls)
	}
	if got := m.outboundByTag["dup"]; got != adapter.Outbound(replacement) {
		t.Fatalf("tag still maps to the closed predecessor: %#v", got)
	}
	if len(m.outbounds) != 1 || m.outbounds[0] != adapter.Outbound(replacement) {
		t.Fatalf("active list not swapped: len=%d", len(m.outbounds))
	}
}

// The happy path must still run every stage and leave the outbound open.
func TestCreateRunsAllStartStages(t *testing.T) {
	t.Parallel()

	out := &lifecycleOutbound{stubOutbound: stubOutbound{tag: "ok"}}
	m := NewManager(logger.NOP(), &stubRegistry{out: out}, nil, "")
	m.started = true

	if err := m.Create(context.Background(), nil, nil, "ok", "stub", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(out.started) != len(adapter.ListStartStages) {
		t.Fatalf("ran %d of %d start stages", len(out.started), len(adapter.ListStartStages))
	}
	if out.closeCalls != 0 {
		t.Fatalf("a successfully started outbound must not be closed (closeCalls=%d)", out.closeCalls)
	}
}
