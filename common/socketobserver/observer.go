// Package socketobserver supplies optional observation hooks for physical outbound sockets.
package socketobserver

import (
	"context"
	"net"
	"sync/atomic"
)

// Label identifies the outbound that owns a socket attempt.
type Label struct {
	Protocol string
	Tag      string
	// DialGroup groups competing interface attempts; zero means ungrouped.
	DialGroup uint64
}

// Observer receives physical dial and UDP socket-setup attempts.
// Implementations must support concurrent calls. Nil completion hooks opt out of an attempt.
// A dial completion may wrap the connection but must preserve its transport semantics.
type Observer interface {
	Begin(Label, string, string) func(net.Conn, error) net.Conn
	BeginPacket(Label, string) func(error)
}

// Binding carries the observer and outbound identity captured at dialer construction.
// Its zero value disables observation.
type (
	Binding struct {
		observer Observer
		label    Label
	}
	contextKey struct{}
)

// FromContext returns the observation binding, or its disabled zero value.
func FromContext(ctx context.Context) Binding { v, _ := ctx.Value(contextKey{}).(Binding); return v }

// WithObserver installs an observer without changing the outbound identity.
func WithObserver(ctx context.Context, observer Observer) context.Context {
	b := FromContext(ctx)
	b.observer = observer
	return context.WithValue(ctx, contextKey{}, b)
}

// WithLabel associates subsequent socket construction with an outbound.
func WithLabel(ctx context.Context, protocol, tag string) context.Context {
	b := FromContext(ctx)
	b.label = Label{Protocol: protocol, Tag: tag}
	return context.WithValue(ctx, contextKey{}, b)
}

// Context propagates the construction-time binding while retaining the request context's cancellation.
func (b Binding) Context(ctx context.Context) context.Context {
	if b.observer == nil && FromContext(ctx).observer == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, b)
}

var nextGroup atomic.Uint64

// NewGroup returns a binding with a fresh process-local interface attempt group.
func (b Binding) NewGroup() Binding {
	if b.observer != nil {
		b.label.DialGroup = nextGroup.Add(1)
	}
	return b
}

// Begin returns a completion hook for a physical dial, or nil when the attempt is not observed.
// Raw socket I/O (including splice) may bypass Read and Write on the returned wrapper.
func (b Binding) Begin(network, endpoint string) func(net.Conn, error) net.Conn {
	if b.observer == nil {
		return nil
	}
	return b.observer.Begin(b.label, network, endpoint)
}

// BeginPacket observes local UDP socket setup, which does not establish peer reachability.
func (b Binding) BeginPacket(endpoint string) func(error) {
	if b.observer == nil {
		return nil
	}
	return b.observer.BeginPacket(b.label, endpoint)
}
