package mux

import (
	"context"
	"net"
)

type sessionConnKey struct{}

// ContextWithSessionConn records conn, the connection a multiplex session runs
// over, in ctx. Every stream of the session inherits it.
func ContextWithSessionConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, sessionConnKey{}, conn)
}

// SessionConnFromContext returns the connection the multiplex session that
// carries ctx's stream runs over, so a handler can read the state of the
// socket shared by all of the session's streams. It reports false for a
// connection that did not arrive over an inbound multiplex session.
func SessionConnFromContext(ctx context.Context) (net.Conn, bool) {
	conn, ok := ctx.Value(sessionConnKey{}).(net.Conn)
	return conn, ok
}
