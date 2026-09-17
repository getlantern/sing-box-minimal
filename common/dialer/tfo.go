package dialer

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/common/socketobserver"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/database64128/tfo-go/v2"
)

type slowOpenConn struct {
	dialer      *tfo.Dialer
	ctx         context.Context
	network     string
	destination M.Socksaddr
	conn        atomic.Pointer[observedTCPConn]
	create      chan struct{}
	done        chan struct{}
	access      sync.Mutex
	closeOnce   sync.Once
	err         error
}

type observedTCPConn struct{ net.Conn }

func (c *slowOpenConn) currentConn() net.Conn {
	v := c.conn.Load()
	if v == nil {
		return nil
	}
	return v.Conn
}

func DialSlowContext(dialer *tfo.Dialer, ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if dialer.DisableTFO || N.NetworkName(network) != N.NetworkTCP {
		switch N.NetworkName(network) {
		case N.NetworkTCP, N.NetworkUDP:
			return dialer.Dialer.DialContext(ctx, network, destination.String())
		default:
			return dialer.Dialer.DialContext(ctx, network, destination.AddrString())
		}
	}
	return &slowOpenConn{
		dialer:      dialer,
		ctx:         ctx,
		network:     network,
		destination: destination,
		create:      make(chan struct{}),
		done:        make(chan struct{}),
	}, nil
}

func (c *slowOpenConn) Read(b []byte) (n int, err error) {
	conn := c.currentConn()
	if conn != nil {
		return conn.Read(b)
	}
	select {
	case <-c.create:
		if c.err != nil {
			return 0, c.err
		}
		return c.currentConn().Read(b)
	case <-c.done:
		return 0, os.ErrClosed
	}
}

func (c *slowOpenConn) Write(b []byte) (n int, err error) {
	tcpConn := c.currentConn()
	if tcpConn != nil {
		return tcpConn.Write(b)
	}
	c.access.Lock()
	defer c.access.Unlock()
	select {
	case <-c.create:
		if c.err != nil {
			return 0, c.err
		}
		return c.currentConn().Write(b)
	case <-c.done:
		return 0, os.ErrClosed
	default:
	}
	done := socketobserver.FromContext(c.ctx).Begin(c.network, c.destination.String())
	conn, err := c.dialer.DialContext(c.ctx, c.network, c.destination.String(), b)
	if done != nil {
		conn = done(conn, err)
	}
	if err != nil {
		c.err = err
	} else {
		c.conn.Store(&observedTCPConn{conn})
	}
	n = len(b)
	close(c.create)
	return
}

func (c *slowOpenConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		conn := c.currentConn()
		if conn != nil {
			conn.Close()
		}
	})
	return nil
}

func (c *slowOpenConn) LocalAddr() net.Addr {
	conn := c.currentConn()
	if conn == nil {
		return M.Socksaddr{}
	}
	return conn.LocalAddr()
}

func (c *slowOpenConn) RemoteAddr() net.Addr {
	conn := c.currentConn()
	if conn == nil {
		return M.Socksaddr{}
	}
	return conn.RemoteAddr()
}

func (c *slowOpenConn) SetDeadline(t time.Time) error {
	conn := c.currentConn()
	if conn == nil {
		return os.ErrInvalid
	}
	return conn.SetDeadline(t)
}

func (c *slowOpenConn) SetReadDeadline(t time.Time) error {
	conn := c.currentConn()
	if conn == nil {
		return os.ErrInvalid
	}
	return conn.SetReadDeadline(t)
}

func (c *slowOpenConn) SetWriteDeadline(t time.Time) error {
	conn := c.currentConn()
	if conn == nil {
		return os.ErrInvalid
	}
	return conn.SetWriteDeadline(t)
}

func (c *slowOpenConn) Upstream() any {
	return c.currentConn()
}

func (c *slowOpenConn) ReaderReplaceable() bool {
	return c.currentConn() != nil
}

func (c *slowOpenConn) WriterReplaceable() bool {
	return c.currentConn() != nil
}

func (c *slowOpenConn) NeedHandshake() bool {
	return c.currentConn() == nil
}

func (c *slowOpenConn) WriteTo(w io.Writer) (n int64, err error) {
	conn := c.currentConn()
	if conn == nil {
		select {
		case <-c.create:
			if c.err != nil {
				return 0, c.err
			}
		case <-c.done:
			return 0, c.err
		}
	}
	return bufio.Copy(w, c.currentConn())
}
