package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/wireguard-go/device"
)

func startUAPIListener(ctx context.Context, name string, wgDevice *device.Device, logger logger.ContextLogger) error {
	uapi, err := uapiListen(name)
	if err != nil {
		return fmt.Errorf("listen UAPI socket: %v", err)
	}

	go func() {
		<-ctx.Done()
		uapi.Close()
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
	return nil
}
