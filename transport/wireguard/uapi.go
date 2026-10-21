package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/wireguard-go/device"
)

// startUAPIListener serves the UAPI socket for wgDevice until the returned
// listener is closed or ctx is done.
func startUAPIListener(ctx context.Context, name string, wgDevice *device.Device, logger logger.ContextLogger) (net.Listener, error) {
	listener, err := uapiListen(name)
	if err != nil {
		return nil, fmt.Errorf("listen UAPI socket: %v", err)
	}
	uapi := newUAPIListener(listener)

	go func() {
		select {
		case <-ctx.Done():
			uapi.Close()
		case <-uapi.closed:
		}
	}()
	go func() {
		for {
			conn, err := uapi.Accept()
			if errors.Is(err, net.ErrClosed) || uapi.isClosed() {
				if conn != nil {
					conn.Close()
				}
				return
			}
			if err != nil {
				logger.Error(E.Cause(err, "uapi accept error"))
				continue // any other accept error, just continue
			}
			if !uapi.track(conn) {
				conn.Close()
				return
			}
			go func() {
				defer uapi.untrack(conn)
				wgDevice.IpcHandle(conn)
			}()
		}
	}()
	return uapi, nil
}

// uapiListener closes the UAPI listener and every connection it accepted,
// once. Closing the connections matters because wireguard-go holds the
// device's IPC lock while reading a set request, and Device.Close needs that
// lock, so a client that stops mid-request would otherwise block shutdown.
// Closing once matters because wireguard-go's listener closes its
// inotify/kqueue descriptors by number on every Close, so a second Close
// could close an unrelated file that reused them.
type uapiListener struct {
	net.Listener
	once   sync.Once
	closed chan struct{}
	err    error
	access sync.Mutex
	conns  map[net.Conn]struct{}
}

func newUAPIListener(listener net.Listener) *uapiListener {
	return &uapiListener{
		Listener: listener,
		closed:   make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}
}

func (l *uapiListener) isClosed() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

// track records conn so Close closes it. It reports false once the listener
// is closed.
func (l *uapiListener) track(conn net.Conn) bool {
	l.access.Lock()
	defer l.access.Unlock()
	if l.isClosed() {
		return false
	}
	l.conns[conn] = struct{}{}
	return true
}

func (l *uapiListener) untrack(conn net.Conn) {
	l.access.Lock()
	defer l.access.Unlock()
	delete(l.conns, conn)
}

func (l *uapiListener) Close() error {
	l.once.Do(func() {
		l.access.Lock()
		close(l.closed)
		conns := l.conns
		l.conns = nil
		l.access.Unlock()
		l.err = l.Listener.Close()
		for conn := range conns {
			conn.Close()
		}
	})
	return l.err
}
