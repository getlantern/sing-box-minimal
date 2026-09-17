package dialer

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/socketobserver"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

func TestNoObserverPreservesSocket(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	d, err := NewDefault(context.Background(), option.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", M.ParseSocksaddr(listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.(*net.TCPConn); !ok {
		t.Fatalf("unobserved connection wrapped: %T", c)
	}
}

type socketTestObserver struct {
	attempts int
	raw      net.Conn
}

func (o *socketTestObserver) Begin(_ socketobserver.Label, _, _ string) func(net.Conn, error) net.Conn {
	o.attempts++
	return func(c net.Conn, _ error) net.Conn { o.raw = c; return c }
}
func (o *socketTestObserver) BeginPacket(socketobserver.Label, string) func(error) { return nil }

type socketTestManager struct {
	adapter.ConnectionManager
	tracked net.Conn
}
type socketTestTracked struct{ net.Conn }

func (m *socketTestManager) TrackConn(c net.Conn) net.Conn {
	m.tracked = c
	return &socketTestTracked{c}
}

func TestObserverSkipsInvalidAddresses(t *testing.T) {
	t.Parallel()
	o := &socketTestObserver{}
	d, err := NewDefault(socketobserver.WithObserver(context.Background(), o), option.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []M.Socksaddr{{}, M.ParseSocksaddr("unresolved.example:443")} {
		if c, err := d.DialContext(context.Background(), "tcp", address); err == nil {
			if c != nil {
				c.Close()
			}
			t.Fatal("invalid address accepted")
		}
	}
	if o.attempts != 0 {
		t.Fatalf("recorded %d nonexistent socket attempts", o.attempts)
	}
}

func TestObserverReceivesSocketBeforeTracking(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	o := &socketTestObserver{}
	d, err := NewDefault(socketobserver.WithObserver(context.Background(), o), option.DialerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	manager := &socketTestManager{}
	d.connectionManager = manager
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", M.ParseSocksaddr(listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.(*socketTestTracked); !ok {
		t.Fatalf("tracking lost: %T", c)
	}
	if _, ok := o.raw.(syscall.Conn); !ok {
		t.Fatalf("observer lost socket handle: %T", o.raw)
	}
	if o.raw != manager.tracked || o.attempts != 1 {
		t.Fatal("observation did not precede tracking")
	}
}

func TestFastOpenUpstreamPreservesConcreteSocket(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	d, err := NewDefault(context.Background(), option.DialerOptions{TCPFastOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", M.ParseSocksaddr(listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	upstream := c.(*slowOpenConn).Upstream()
	if _, ok := upstream.(*net.TCPConn); !ok {
		t.Fatalf("concrete socket hidden: %T", upstream)
	}
	if _, ok := upstream.(syscall.Conn); !ok {
		t.Fatalf("socket interface hidden: %T", upstream)
	}
}
