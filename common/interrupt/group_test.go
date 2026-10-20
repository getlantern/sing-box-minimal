package interrupt

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type countingCloser struct{ closes atomic.Int32 }

func (c *countingCloser) Close() error { c.closes.Add(1); return nil }

func TestGroupTrack(t *testing.T) {
	g := NewGroup()
	tracked, untracked, internal := &countingCloser{}, &countingCloser{}, &countingCloser{}
	g.Track(tracked, true)
	untrack := g.Track(untracked, true)
	untrack()
	untrack()
	g.Track(internal, false)
	if g.Len() != 2 {
		t.Fatalf("Len = %d, want 2", g.Len())
	}

	g.Interrupt(false)
	if tracked.closes.Load() != 0 || internal.closes.Load() != 1 {
		t.Fatalf("Interrupt(false) must close only internal conns: external=%d internal=%d",
			tracked.closes.Load(), internal.closes.Load())
	}
	g.Interrupt(true)
	g.Interrupt(true)
	if tracked.closes.Load() != 1 {
		t.Fatalf("tracked closer closed %d times, want 1", tracked.closes.Load())
	}
	if untracked.closes.Load() != 0 {
		t.Fatal("untracked closer must not be closed")
	}
	if g.Len() != 0 {
		t.Fatalf("Len = %d after Interrupt, want 0", g.Len())
	}
}

func TestGroupInterruptClosesOutsideLock(t *testing.T) {
	g := NewGroup()
	// A conn whose Close re-enters the group, as an interrupt.Conn of g would.
	reentrant := closerFunc(func() error { g.Track(&countingCloser{}, true)(); return nil })
	g.Track(reentrant, true)
	done := make(chan struct{})
	go func() { g.Interrupt(true); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Interrupt deadlocked on a Close that re-enters the group")
	}
}

func waitCloses(t *testing.T, c *countingCloser, want int32, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.closes.Load() != want {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func TestGroupRetireClosesLateTracks(t *testing.T) {
	g := NewGroup()
	early, late := &countingCloser{}, &countingCloser{}
	g.Track(early, true)
	g.Retire()
	if !g.Retired() {
		t.Fatal("Retire must mark the group retired before returning")
	}
	waitCloses(t, early, 1, "Retire must close tracked conns")
	untrack := g.Track(late, true)
	untrack()
	waitCloses(t, late, 1, "tracking on a retired group must close the conn")
	local, peer := net.Pipe()
	defer peer.Close()
	wrapped := g.NewConn(local, true)
	if _, err := wrapped.Read(make([]byte, 1)); err == nil {
		t.Fatal("NewConn on a retired group must return a closed conn")
	}
	if g.Len() != 0 {
		t.Fatalf("Len = %d on a retired group, want 0", g.Len())
	}

	plain := NewGroup()
	plain.Interrupt(true)
	plain.Track(&countingCloser{}, true)
	if plain.Retired() || plain.Len() != 1 {
		t.Fatal("Interrupt must not retire the group")
	}
	if !g.Retired() {
		t.Fatal("Retire must mark the group retired")
	}
}

func TestGroupTryNewConn(t *testing.T) {
	g := NewGroup()
	local, peer := net.Pipe()
	defer peer.Close()
	wrapped, ok := g.TryNewConn(local, true)
	if !ok || wrapped == nil || g.Len() != 1 {
		t.Fatal("TryNewConn must wrap on a live group")
	}
	g.Retire()
	late, latePeer := net.Pipe()
	defer latePeer.Close()
	defer late.Close()
	if c, ok := g.TryNewConn(late, true); ok || c != nil {
		t.Fatal("TryNewConn must refuse on a retired group")
	}
	go late.Write([]byte("x"))
	_ = latePeer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := latePeer.Read(make([]byte, 1)); err != nil {
		t.Fatal("a refused conn must be left open for the caller to close")
	}
}

func TestContextWithGroupIgnoresNil(t *testing.T) {
	if len(GroupsFromContext(ContextWithGroup(context.Background(), nil))) != 0 {
		t.Fatal("nil group must not be added")
	}
}

func TestGroupsFromContext(t *testing.T) {
	if len(GroupsFromContext(context.Background())) != 0 {
		t.Fatal("no groups expected")
	}
	// Three deep, so the stored slice can have spare capacity siblings might share.
	base := context.Background()
	for range 3 {
		base = ContextWithGroup(base, NewGroup())
	}
	left, right := NewGroup(), NewGroup()
	leftCtx := ContextWithGroup(base, left)
	rightCtx := ContextWithGroup(base, right)
	if got := GroupsFromContext(leftCtx); len(got) != 4 || got[3] != left {
		t.Fatalf("sibling append clobbered the left context: %v", got)
	}
	if got := GroupsFromContext(rightCtx); len(got) != 4 || got[3] != right {
		t.Fatalf("right context lost its group: %v", got)
	}
	_ = append(GroupsFromContext(leftCtx), NewGroup())
	GroupsFromContext(rightCtx)[3] = nil
	if GroupsFromContext(rightCtx)[3] != right {
		t.Fatal("changing a returned slice must not write into stored contexts")
	}
	if got := GroupsFromContext(ContextWithGroup(leftCtx, left)); len(got) != 4 {
		t.Fatalf("re-adding a group must not track flows twice, got %d groups", len(got))
	}
}
