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
	uapi := &onceCloseListener{Listener: listener, closed: make(chan struct{})}

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
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				logger.Error(E.Cause(err, "uapi accept error"))
				continue // any other accept error, just continue
			}
			go wgDevice.IpcHandle(conn)
		}
	}()
	return uapi, nil
}

// onceCloseListener makes Close idempotent. wireguard-go's UAPI listener
// closes its inotify/kqueue descriptors by number on every Close, so a second
// Close could close an unrelated file that reused them.
type onceCloseListener struct {
	net.Listener
	once   sync.Once
	closed chan struct{}
	err    error
}

func (l *onceCloseListener) Close() error {
	l.once.Do(func() {
		l.err = l.Listener.Close()
		close(l.closed)
	})
	return l.err
}
