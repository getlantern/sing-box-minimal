package outbound

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

type stubOutbound struct {
	tag string
}

func (s *stubOutbound) Type() string           { return "stub" }
func (s *stubOutbound) Tag() string            { return s.tag }
func (s *stubOutbound) Network() []string      { return nil }
func (s *stubOutbound) Dependencies() []string { return nil }
func (s *stubOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, nil
}
func (s *stubOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

// Close() nils the outbounds slice but leaves outboundByTag populated, so a
// Remove racing shutdown used to hit panic("invalid inbound index"). It must
// instead drop the stale map entry and return nil.
func TestRemoveAfterClose(t *testing.T) {
	m := NewManager(logger.NOP(), nil, nil, "")
	out := &stubOutbound{tag: "test-out"}
	m.outbounds = append(m.outbounds, out)
	m.outboundByTag[out.Tag()] = out
	m.started = true

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Remove("test-out"); err != nil {
		t.Fatalf("Remove after Close: %v", err)
	}
	if _, found := m.outboundByTag["test-out"]; found {
		t.Fatal("stale outboundByTag entry not cleaned up")
	}
	if err := m.Remove("test-out"); err == nil {
		t.Fatal("second Remove should report the tag as gone")
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
