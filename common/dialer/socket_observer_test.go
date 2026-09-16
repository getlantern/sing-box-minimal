package dialer

import (
	"context"
	"net"
	"testing"
	"time"

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
