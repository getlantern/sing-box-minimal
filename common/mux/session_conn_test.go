package mux

import (
	"context"
	"net"
	"testing"
)

func TestSessionConnFromContext(t *testing.T) {
	if _, ok := SessionConnFromContext(context.Background()); ok {
		t.Fatal("found a session conn in a context without one")
	}
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	ctx := ContextWithSessionConn(context.Background(), conn)
	// Streams get a context derived from the session's.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	got, ok := SessionConnFromContext(ctx)
	if !ok || got != conn {
		t.Fatalf("SessionConnFromContext = %v, %t; want the session conn", got, ok)
	}
}
