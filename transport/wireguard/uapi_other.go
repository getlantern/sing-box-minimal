//go:build !windows

package wireguard

import (
	"fmt"
	"net"

	"github.com/sagernet/wireguard-go/ipc"
)

func uapiListen(name string) (net.Listener, error) {
	fileUAPI, uapiErr := ipc.UAPIOpen(name)
	if uapiErr != nil {
		return nil, fmt.Errorf("failed to open uapi socket for %s: %w", name, uapiErr)
	}
	// UAPIListen wraps a duplicate of this descriptor, so release the original.
	defer fileUAPI.Close()

	uapi, err := ipc.UAPIListen(name, fileUAPI)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on uapi socket: %v", err)
	}
	return uapi, nil
}
