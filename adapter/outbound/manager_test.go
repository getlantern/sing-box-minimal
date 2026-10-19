package outbound

import (
	"context"
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
