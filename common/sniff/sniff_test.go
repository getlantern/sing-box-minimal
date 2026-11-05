package sniff_test

import (
	"context"
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/buf"

	"github.com/stretchr/testify/require"
)

// A second sniff rule must classify bytes an earlier sniff already read.
// The client sends a whole BitTorrent handshake and then waits for the
// peer's, so no more bytes arrive.
func TestPeekStreamSniffsBufferedBytes(t *testing.T) {
	t.Parallel()

	handshake, err := hex.DecodeString("13426974546f7272656e742070726f746f636f6c0000000000100000e21ea9569b69bab33c97851d0298bdfa89bc90922d5554313631302dea812fcd6a3563e3be40c1d1")
	require.NoError(t, err)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _, _ = client.Write(handshake) }()

	var metadata adapter.InboundContext
	first := buf.NewPacket()
	defer first.Release()
	err = sniff.PeekStream(context.Background(), &metadata, server, nil, first, 100*time.Millisecond, sniff.HTTPHost)
	require.Error(t, err)
	require.Equal(t, len(handshake), first.Len())

	second := buf.NewPacket()
	defer second.Release()
	err = sniff.PeekStream(context.Background(), &metadata, server, []*buf.Buffer{first}, second, 100*time.Millisecond, sniff.BitTorrent)
	require.NoError(t, err)
	require.Equal(t, C.ProtocolBitTorrent, metadata.Protocol)
	require.True(t, second.IsEmpty())
}
