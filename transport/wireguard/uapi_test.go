package wireguard

import (
	"net"
	"sync/atomic"
	"testing"

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

func TestOnceCloseListenerClosesUnderlyingOnce(t *testing.T) {
	inner := &countingListener{}
	l := &onceCloseListener{Listener: inner, closed: make(chan struct{})}
	require.NoError(t, l.Close())
	require.NoError(t, l.Close())
	require.Equal(t, int32(1), inner.closes.Load())
	select {
	case <-l.closed:
	default:
		t.Fatal("closed channel not closed")
	}
}
