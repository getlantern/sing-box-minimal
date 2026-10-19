package wireguard

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countingListener struct {
	net.Listener
	closes atomic.Int32
}

func (l *countingListener) Close() error {
	l.closes.Add(1)
	return nil
}

func TestUAPIListenerClosesUnderlyingOnce(t *testing.T) {
	inner := &countingListener{}
	l := newUAPIListener(inner)
	require.NoError(t, l.Close())
	require.NoError(t, l.Close())
	require.Equal(t, int32(1), inner.closes.Load())
	require.True(t, l.isClosed())
}

func TestUAPIListenerCloseClosesAcceptedConns(t *testing.T) {
	l := newUAPIListener(&countingListener{})
	server, client := net.Pipe()
	t.Cleanup(func() { client.Close() })
	require.True(t, l.track(server))

	readErr := make(chan error, 1)
	go func() {
		// Stands in for IpcHandle blocked reading an unfinished set request.
		_, err := server.Read(make([]byte, 1))
		readErr <- err
	}()
	require.NoError(t, l.Close())
	select {
	case err := <-readErr:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close left an accepted connection open")
	}

	late, lateClient := net.Pipe()
	t.Cleanup(func() { late.Close(); lateClient.Close() })
	require.False(t, l.track(late), "connections accepted after Close must not be tracked")
}
