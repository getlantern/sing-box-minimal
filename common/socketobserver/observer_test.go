package socketobserver

import (
	"context"
	"errors"
	"net"
	"testing"
)

type testObserver struct {
	label     Label
	dialErr   error
	packetErr error
}

func (o *testObserver) Begin(label Label, network, endpoint string) func(net.Conn, error) net.Conn {
	o.label = label
	return func(conn net.Conn, err error) net.Conn { o.dialErr = err; return conn }
}

func (o *testObserver) BeginPacket(label Label, endpoint string) func(error) {
	o.label = label
	return func(err error) { o.packetErr = err }
}

func TestBindingIsolation(t *testing.T) {
	t.Parallel()
	first, second := &testObserver{}, &testObserver{}
	a := FromContext(WithLabel(WithObserver(context.Background(), first), "tls", "first"))
	b := FromContext(WithLabel(WithObserver(context.Background(), second), "socks", "second"))
	failure := errors.New("dial failed")
	a.Begin("tcp", "example:443")(nil, failure)
	b.BeginPacket("example:443")(failure)
	if first.label.Tag != "first" || second.label.Tag != "second" || first.dialErr != failure || second.packetErr != failure {
		t.Fatal("observer bindings mixed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	carried := a.Context(ctx)
	if carried.Err() != context.Canceled || FromContext(carried).observer != first {
		t.Fatal("binding lost cancellation or observer")
	}
}

func TestDisabledBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := FromContext(ctx)
	if b.Begin("tcp", "example:443") != nil || b.BeginPacket("example:443") != nil || b.Context(ctx) != ctx {
		t.Fatal("disabled observer affected a dial")
	}
}

func TestDisabledBindingMasksRequestObserver(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(WithObserver(context.Background(), &testObserver{}))
	cancel()
	carried := (Binding{}).Context(ctx)
	b := FromContext(carried)
	if carried.Err() != context.Canceled || b.Begin("tcp", "example:443") != nil || b.BeginPacket("example:443") != nil {
		t.Fatal("disabled construction binding inherited request observer")
	}
}
