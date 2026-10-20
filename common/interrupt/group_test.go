package interrupt

import (
	"context"
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

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

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
