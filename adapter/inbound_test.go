package adapter

import (
	"net"
	"net/netip"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func TestDNSResponseAddressesUnmapsHTTPSIPv4Hints(t *testing.T) {
	t.Parallel()

	ipv4Hint := net.ParseIP("1.1.1.1")
	require.NotNil(t, ipv4Hint)

	response := &dns.Msg{
		MsgHdr: dns.MsgHdr{
			Response: true,
			Rcode:    dns.RcodeSuccess,
		},
		Answer: []dns.RR{
			&dns.HTTPS{
				SVCB: dns.SVCB{
					Hdr: dns.RR_Header{
						Name:   dns.Fqdn("example.com"),
						Rrtype: dns.TypeHTTPS,
						Class:  dns.ClassINET,
						Ttl:    60,
					},
					Priority: 1,
					Target:   ".",
					Value: []dns.SVCBKeyValue{
						&dns.SVCBIPv4Hint{Hint: []net.IP{ipv4Hint}},
					},
				},
			},
		},
	}

	addresses := DNSResponseAddresses(response)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("1.1.1.1")}, addresses)
	require.True(t, addresses[0].Is4())
}

func TestNormalizedProcessPath(t *testing.T) {
	t.Parallel()
	var metadata InboundContext
	require.Empty(t, metadata.NormalizedProcessPath())
	metadata.ProcessInfo = &ConnectionOwner{}
	require.Empty(t, metadata.NormalizedProcessPath())

	for _, path := range []string{"first.exe", "second.exe", ""} {
		metadata.ProcessInfo.ProcessPath = path
		require.Equal(t, path, metadata.NormalizedProcessPath())
		require.Equal(t, path, metadata.ProcessInfo.ProcessPath)
	}
	metadata.ProcessInfo.ProcessPath = "first.exe"
	require.Equal(t, "first.exe", metadata.NormalizedProcessPath())
	metadata.ProcessInfo = nil
	require.Empty(t, metadata.NormalizedProcessPath())
}
