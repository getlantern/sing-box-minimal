//go:build darwin

package process

import (
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDarwinDualStackUDPWildcard(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			inp := make([]byte, darwinXsocketOffset)
			so := make([]byte, structSize-darwinXsocketOffset)
			inp[darwinXinpcbVFlag] = test.flags
			binary.BigEndian.PutUint16(inp[darwinXinpcbLocalPort:], 12345)
			binary.NativeEndian.PutUint32(so[darwinXsocketLastPID:], 42)
			entry, ok := parseDarwinConnectionEntry(inp, so)
			require.True(t, ok, "socket was not parsed")
			for _, source := range []string{"127.0.0.1:12345", "[::1]:12345"} {
				address := netip.MustParseAddrPort(source)
				owner, kind, err := matchDarwinConnectionEntry([]darwinConnectionEntry{entry}, test.network, address, netip.AddrPort{})
				wantFound := address.Addr().Is4() && test.want4 || address.Addr().Is6() && test.want6
				if !wantFound {
					require.ErrorIs(t, err, ErrNotFound, "source: %s", source)
					continue
				}
				require.NoError(t, err, "source: %s", source)
				require.Equal(t, uint32(42), owner.pid)
				require.Equal(t, darwinConnectionMatchWildcardFallback, kind)
			}
		})
	}
}

func TestDarwinDualStackUDPMatchLimits(t *testing.T) {
	t.Parallel()
	source := netip.MustParseAddrPort("[::1]:12345")
	destination := netip.MustParseAddrPort("[::1]:443")
	for _, test := range []struct {
		name       string
		localAddr  string
		localPort  uint16
		remotePort uint16
	}{
		{name: "bound IPv4 address", localAddr: "127.0.0.1", localPort: 12345},
		{name: "connected socket", localAddr: "0.0.0.0", localPort: 12345, remotePort: 443},
		{name: "different local port", localAddr: "0.0.0.0", localPort: 12346},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			entry := darwinConnectionEntry{
				localAddr:  netip.MustParseAddr(test.localAddr),
				localPort:  test.localPort,
				remotePort: test.remotePort,
				dualStack:  true,
			}
			_, _, err := matchDarwinConnectionEntry([]darwinConnectionEntry{entry}, "udp", source, destination)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestDarwinDualStackUDPExactMatch(t *testing.T) {
	t.Parallel()
	source := netip.MustParseAddrPort("[::1]:12345")
	destination := netip.MustParseAddrPort("[::1]:443")
	entries := []darwinConnectionEntry{
		{localAddr: netip.IPv4Unspecified(), localPort: source.Port(), dualStack: true, pid: 41},
		{localAddr: source.Addr(), localPort: source.Port(), remoteAddr: destination.Addr(), remotePort: destination.Port(), pid: 42},
	}
	owner, kind, err := matchDarwinConnectionEntry(entries, "udp", source, destination)
	require.NoError(t, err)
	require.Equal(t, uint32(42), owner.pid)
	require.Equal(t, darwinConnectionMatchExact, kind)
}

func TestDarwinDualStackUDPSocket(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	require.NoError(t, err)
	executable, err = filepath.EvalSymlinks(executable)
	require.NoError(t, err)
	for _, host := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			server, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort(host)))
			require.NoError(t, err)
			defer server.Close()
			conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("[::]:0")))
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetWriteDeadline(time.Now().Add(time.Second)))
			require.NoError(t, server.SetReadDeadline(time.Now().Add(time.Second)))

			destination := server.LocalAddr().(*net.UDPAddr).AddrPort()
			_, err = conn.WriteToUDPAddrPort([]byte("owner lookup"), destination)
			require.NoError(t, err)
			buffer := make([]byte, 64)
			n, source, err := server.ReadFromUDPAddrPort(buffer)
			require.NoError(t, err)
			require.Equal(t, "owner lookup", string(buffer[:n]))

			owner, err := FindDarwinConnectionOwner("udp", source, destination)
			require.NoError(t, err)
			require.Equal(t, executable, owner.ProcessPath)
			require.Equal(t, int32(os.Getuid()), owner.UserId)
		})
	}
}
