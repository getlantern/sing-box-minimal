//go:build darwin

package process

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinDualStackUDPWildcard(t *testing.T) {
	for _, test := range []struct {
		name    string
		flags   byte
		network string
		want4   bool
		want6   bool
	}{
		{name: "dual stack UDP", flags: 3, network: "udp", want4: true, want6: true},
		{name: "IPv4 only UDP", flags: 1, network: "udp", want4: true},
		{name: "IPv6 only UDP", flags: 2, network: "udp", want6: true},
		{name: "TCP has no wildcard fallback", flags: 3, network: "tcp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inp := make([]byte, darwinXsocketOffset)
			so := make([]byte, structSize-darwinXsocketOffset)
			inp[darwinXinpcbVFlag] = test.flags
			binary.BigEndian.PutUint16(inp[darwinXinpcbLocalPort:], 12345)
			binary.NativeEndian.PutUint32(so[darwinXsocketLastPID:], 42)
			entry, ok := parseDarwinConnectionEntry(inp, so)
			if !ok {
				t.Fatal("socket was not parsed")
			}
			for _, source := range []string{"127.0.0.1:12345", "[::1]:12345"} {
				address := netip.MustParseAddrPort(source)
				owner, _, err := matchDarwinConnectionEntry([]darwinConnectionEntry{entry}, test.network, address, netip.AddrPort{})
				wantFound := address.Addr().Is4() && test.want4 || address.Addr().Is6() && test.want6
				if !wantFound {
					if !errors.Is(err, ErrNotFound) {
						t.Fatalf("unexpected match for %s: owner=%+v, err=%v", source, owner, err)
					}
					continue
				}
				if err != nil || owner.pid != 42 {
					t.Fatalf("lookup %s: owner=%+v, err=%v", source, owner, err)
				}
			}
		})
	}
}

func TestDarwinDualStackUDPSocket(t *testing.T) {
	conn, err := net.ListenPacket("udp", "[::]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	destination := netip.MustParseAddrPort("[::1]:12345")
	if _, err := conn.WriteTo([]byte("owner lookup"), net.UDPAddrFromAddrPort(destination)); err != nil {
		t.Fatal(err)
	}
	source := netip.AddrPortFrom(netip.IPv6Loopback(), uint16(conn.LocalAddr().(*net.UDPAddr).Port))
	owner, err := FindDarwinConnectionOwner("udp", source, destination)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	if owner.ProcessPath != executable || owner.UserId != int32(os.Getuid()) {
		t.Fatalf("owner = %+v, want uid=%d path=%s", owner, os.Getuid(), executable)
	}
}
