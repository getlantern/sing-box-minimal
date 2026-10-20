package interrupt

import (
	"io"
	"net"
	"sync"

	"github.com/sagernet/sing/common/x/list"
)

type Group struct {
	access      sync.Mutex
	connections list.List[*groupConnItem]
	retired     bool
}

type groupConnItem struct {
	conn       io.Closer
	isExternal bool
}

func NewGroup() *Group {
	return &Group{}
}

func (g *Group) NewConn(conn net.Conn, isExternal bool) net.Conn {
	g.access.Lock()
	defer g.access.Unlock()
	if g.retired {
		go conn.Close()
		return conn
	}
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return &Conn{Conn: conn, group: g, element: item}
}

func (g *Group) NewPacketConn(conn net.PacketConn, isExternal bool) net.PacketConn {
	g.access.Lock()
	defer g.access.Unlock()
	if g.retired {
		go conn.Close()
		return conn
	}
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return &PacketConn{PacketConn: conn, group: g, element: item}
}

// TryNewConn wraps conn like NewConn, or reports false without wrapping or
// closing it if the group is retired, in one step so a caller can fail its
// dial instead of returning a conn that is about to close.
func (g *Group) TryNewConn(conn net.Conn, isExternal bool) (net.Conn, bool) {
	g.access.Lock()
	defer g.access.Unlock()
	if g.retired {
		return nil, false
	}
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return &Conn{Conn: conn, group: g, element: item}, true
}

// TryNewPacketConn is TryNewConn for packet conns.
func (g *Group) TryNewPacketConn(conn net.PacketConn, isExternal bool) (net.PacketConn, bool) {
	g.access.Lock()
	defer g.access.Unlock()
	if g.retired {
		return nil, false
	}
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return &PacketConn{PacketConn: conn, group: g, element: item}, true
}

// Track registers closer without wrapping it, for conns whose wrapper would hide
// interfaces callers need. untrack is safe to call more than once.
// On a retired group, closer is closed at once, off the caller's goroutine.
func (g *Group) Track(closer io.Closer, isExternal bool) (untrack func()) {
	g.access.Lock()
	if g.retired {
		g.access.Unlock()
		go closer.Close()
		return func() {}
	}
	element := g.connections.PushBack(&groupConnItem{closer, isExternal})
	g.access.Unlock()
	return func() {
		g.access.Lock()
		g.connections.Remove(element)
		g.access.Unlock()
	}
}

// Retired reports whether Retire has run.
func (g *Group) Retired() bool {
	g.access.Lock()
	defer g.access.Unlock()
	return g.retired
}

// Len reports how many conns the group tracks.
func (g *Group) Len() int {
	g.access.Lock()
	defer g.access.Unlock()
	return g.connections.Len()
}

// Interrupt closes the matching conns concurrently, after releasing the lock,
// so a slow or re-entrant Close can't stall the group or the other conns. A
// concurrent Interrupt may return before the other one's closes finish.
func (g *Group) Interrupt(interruptExternalConnections bool) {
	closeAll(g.take(interruptExternalConnections, false))
}

// Retire marks the group retired before returning, so conns tracked or wrapped
// afterwards are closed at once, then closes its current conns in the
// background. For groups replaced wholesale: conns whose dial or async dispatch
// outlives the switch are still closed, and callers holding a lock don't block
// on a slow Close.
func (g *Group) Retire() {
	if closers := g.take(true, true); len(closers) > 0 {
		go closeAll(closers)
	}
}

func (g *Group) take(interruptExternalConnections bool, retire bool) []io.Closer {
	g.access.Lock()
	defer g.access.Unlock()
	if retire {
		g.retired = true
	}
	var toClose []io.Closer
	for element := g.connections.Front(); element != nil; {
		next := element.Next()
		if !element.Value.isExternal || interruptExternalConnections {
			toClose = append(toClose, element.Value.conn)
			g.connections.Remove(element)
		}
		element = next
	}
	return toClose
}

func closeAll(closers []io.Closer) {
	if len(closers) == 1 {
		closers[0].Close()
		return
	}
	var wg sync.WaitGroup
	for _, conn := range closers {
		wg.Go(func() { conn.Close() })
	}
	wg.Wait()
}
