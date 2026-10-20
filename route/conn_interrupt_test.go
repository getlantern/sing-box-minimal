package route

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type countingCloseConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *countingCloseConn) Close() error { c.closes.Add(1); return c.Conn.Close() }

// CloseWrite keeps half-close support so the copy loop half-closes rather than closes.
func (c *countingCloseConn) CloseWrite() error { return c.Conn.(*net.TCPConn).CloseWrite() }

type stubDialer struct {
	conn       net.Conn
	packetConn net.PacketConn
}

func (d *stubDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return d.conn, nil
}

func (d *stubDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return d.packetConn, nil
}

// tcpPair returns the two ends of a loopback TCP connection.
func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	dialed, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	server := <-accepted
	require.NotNil(t, server)
	t.Cleanup(func() { dialed.Close(); server.Close() })
	return dialed.(*net.TCPConn), server.(*net.TCPConn)
}

func newFlow(t *testing.T, g *interrupt.Group) (client, upstream *net.TCPConn, remote *countingCloseConn, closed chan error) {
	t.Helper()
	client, inbound := tcpPair(t)
	remoteLocal, upstream := tcpPair(t)
	remote = &countingCloseConn{Conn: remoteLocal}
	closed = make(chan error, 1)
	ctx := interrupt.ContextWithIsExternalConnection(context.Background())
	if g != nil {
		ctx = interrupt.ContextWithGroup(ctx, g)
	}
	m := NewConnectionManager(log.NewNOPFactory().Logger())
	m.NewConnection(ctx, &stubDialer{conn: remote}, inbound, adapter.InboundContext{
		Destination: M.ParseSocksaddr("example.com:443"),
	}, func(err error) { closed <- err })
	return client, upstream, remote, closed
}

func waitClosed(t *testing.T, closed chan error) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("flow was not torn down")
	}
}

func TestConnectionManager_InterruptClosesTrackedRemote(t *testing.T) {
	g := interrupt.NewGroup()
	client, upstream, remote, closed := newFlow(t, g)

	_, err := client.Write([]byte("hi"))
	require.NoError(t, err)
	buf := make([]byte, 2)
	_, err = upstream.Read(buf)
	require.NoError(t, err)

	g.Interrupt(true)
	require.Positive(t, remote.closes.Load(), "the remote must be closed by the time Interrupt returns")
	waitClosed(t, closed)
}

type blockingCloseConn struct {
	net.Conn
	release chan struct{}
}

func (c *blockingCloseConn) Close() error { <-c.release; return c.Conn.Close() }

func TestConnectionManager_StuckInboundCloseKeepsNoRemoteOpen(t *testing.T) {
	g := interrupt.NewGroup()
	m := NewConnectionManager(log.NewNOPFactory().Logger())
	var remotes []*countingCloseConn
	var stuck []*blockingCloseConn
	for range 3 {
		_, inboundRaw := tcpPair(t)
		remoteLocal, _ := tcpPair(t)
		inbound := &blockingCloseConn{Conn: inboundRaw, release: make(chan struct{})}
		remote := &countingCloseConn{Conn: remoteLocal}
		m.NewConnection(interrupt.ContextWithGroup(context.Background(), g), &stubDialer{conn: remote}, inbound,
			adapter.InboundContext{Destination: M.ParseSocksaddr("example.com:443")}, func(error) {})
		remotes = append(remotes, remote)
		stuck = append(stuck, inbound)
	}
	t.Cleanup(func() {
		for _, s := range stuck {
			close(s.release)
		}
	})

	go g.Interrupt(true)
	require.Eventually(t, func() bool {
		for _, r := range remotes {
			if r.closes.Load() == 0 {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "stuck inbound Closes must not keep any retired remote open")
}

func TestConnectionManager_InterruptClosesHalfClosedFlow(t *testing.T) {
	g := interrupt.NewGroup()
	client, upstream, _, closed := newFlow(t, g)

	// The upload finishes and CloseWrites the remote; only the download is left,
	// blocked reading from the old remote.
	require.NoError(t, client.CloseWrite())
	require.NoError(t, upstream.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err := upstream.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF, "upstream should see the half-close")

	g.Interrupt(true)
	waitClosed(t, closed)
}

func TestConnectionManager_InterruptClosesFlowAfterServerHalfClose(t *testing.T) {
	g := interrupt.NewGroup()
	client, upstream, _, closed := newFlow(t, g)

	// The download finishes and CloseWrites the inbound; only the upload is left,
	// blocked reading from the idle client.
	require.NoError(t, upstream.CloseWrite())
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err := client.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF, "client should see the half-close")

	g.Interrupt(true)
	waitClosed(t, closed)
}

func TestConnectionManager_NestedGroupsEachTrack(t *testing.T) {
	outer, inner := interrupt.NewGroup(), interrupt.NewGroup()
	ctx := interrupt.ContextWithGroup(interrupt.ContextWithGroup(context.Background(), outer), inner)
	m := NewConnectionManager(log.NewNOPFactory().Logger())
	newNestedFlow := func() (net.Conn, chan error) {
		client, inbound := tcpPair(t)
		remoteLocal, _ := tcpPair(t)
		closed := make(chan error, 1)
		m.NewConnection(ctx, &stubDialer{conn: remoteLocal}, inbound, adapter.InboundContext{
			Destination: M.ParseSocksaddr("example.com:443"),
		}, func(err error) { closed <- err })
		return client, closed
	}

	_, closedByOuter := newNestedFlow()
	_, closedByInner := newNestedFlow()
	require.Equal(t, 2, outer.Len())
	require.Equal(t, 2, inner.Len())

	inner.Interrupt(true)
	waitClosed(t, closedByOuter)
	waitClosed(t, closedByInner)
	require.Eventually(t, func() bool { return outer.Len() == 0 }, 5*time.Second, 10*time.Millisecond,
		"interrupting one group must untrack the flow from every group")

	client, closed := newNestedFlow()
	outer.Interrupt(true)
	waitClosed(t, closed)
	require.Eventually(t, func() bool { return inner.Len() == 0 }, 5*time.Second, 10*time.Millisecond)
	client.Close()
}

func TestConnectionManager_RetiredGroupFailsLateFlowHandshake(t *testing.T) {
	g := interrupt.NewGroup()
	g.Retire()
	_, _, remote, closed := newFlow(t, g)
	select {
	case err := <-closed:
		require.ErrorContains(t, err, "route retired", "a late flow must fail its handshake, not connect then reset")
	case <-time.After(5 * time.Second):
		t.Fatal("late flow was not refused")
	}
	require.Positive(t, remote.closes.Load())
	require.Zero(t, g.Len())
}

func TestConnectionManager_FinishedFlowIsUntracked(t *testing.T) {
	g := interrupt.NewGroup()
	client, upstream, _, closed := newFlow(t, g)
	require.Equal(t, 1, g.Len())

	require.NoError(t, client.Close())
	require.NoError(t, upstream.Close())
	waitClosed(t, closed)
	require.Zero(t, g.Len(), "a finished flow must leave the group")
}

func TestConnectionManager_InterruptClosesTrackedPacketRemote(t *testing.T) {
	g := interrupt.NewGroup()
	inbound, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { inbound.Close() })
	remote, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { remote.Close() })

	closed := make(chan error, 1)
	ctx := interrupt.ContextWithGroup(interrupt.ContextWithIsExternalConnection(context.Background()), g)
	m := NewConnectionManager(log.NewNOPFactory().Logger())
	m.NewPacketConnection(ctx, &stubDialer{packetConn: remote}, bufio.NewPacketConn(inbound), adapter.InboundContext{
		Destination: M.ParseSocksaddr("127.0.0.1:53"),
	}, func(err error) { closed <- err })

	g.Interrupt(true)
	waitClosed(t, closed)
}

func TestConnectionManager_FinishedPacketFlowIsUntracked(t *testing.T) {
	g := interrupt.NewGroup()
	inbound, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	remote, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { remote.Close() })

	closed := make(chan error, 1)
	ctx := interrupt.ContextWithGroup(context.Background(), g)
	m := NewConnectionManager(log.NewNOPFactory().Logger())
	m.NewPacketConnection(ctx, &stubDialer{packetConn: remote}, bufio.NewPacketConn(inbound), adapter.InboundContext{
		Destination: M.ParseSocksaddr("127.0.0.1:53"),
	}, func(err error) { closed <- err })
	require.Equal(t, 1, g.Len())

	require.NoError(t, inbound.Close())
	waitClosed(t, closed)
	require.Zero(t, g.Len(), "a finished packet flow must leave the group")
}

func TestConnectionManager_NoGroupLeavesFlowAlone(t *testing.T) {
	// A group on a sibling branch of the flow's ctx must not reach the flow.
	base := interrupt.ContextWithIsExternalConnection(context.Background())
	g := interrupt.NewGroup()
	_ = interrupt.ContextWithGroup(base, g)
	client, inbound := tcpPair(t)
	remoteLocal, upstream := tcpPair(t)
	closed := make(chan error, 1)
	m := NewConnectionManager(log.NewNOPFactory().Logger())
	m.NewConnection(base, &stubDialer{conn: remoteLocal}, inbound, adapter.InboundContext{
		Destination: M.ParseSocksaddr("example.com:443"),
	}, func(err error) { closed <- err })
	require.Zero(t, g.Len())

	g.Interrupt(true)
	select {
	case err := <-closed:
		t.Fatalf("flow without a group was closed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	_, err := client.Write([]byte("ok"))
	require.NoError(t, err)
	buf := make([]byte, 2)
	_, err = upstream.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "ok", string(buf))
}
