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
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return &Conn{Conn: conn, group: g, element: item}
}

func (g *Group) NewPacketConn(conn net.PacketConn, isExternal bool) net.PacketConn {
	g.access.Lock()
	defer g.access.Unlock()
	item := g.connections.PushBack(&groupConnItem{conn, isExternal})
	return &PacketConn{PacketConn: conn, group: g, element: item}
}

// Track registers closer without wrapping it, for conns whose wrapper would hide
// interfaces callers need. untrack is safe to call more than once.
func (g *Group) Track(closer io.Closer, isExternal bool) (untrack func()) {
	g.access.Lock()
	element := g.connections.PushBack(&groupConnItem{closer, isExternal})
	g.access.Unlock()
	return func() {
		g.access.Lock()
		g.connections.Remove(element)
		g.access.Unlock()
	}
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
	g.access.Lock()
	var toClose []io.Closer
	for element := g.connections.Front(); element != nil; {
		next := element.Next()
		if !element.Value.isExternal || interruptExternalConnections {
			toClose = append(toClose, element.Value.conn)
			g.connections.Remove(element)
		}
		element = next
	}
	g.access.Unlock()
	if len(toClose) == 1 {
		toClose[0].Close()
		return
	}
	var wg sync.WaitGroup
	for _, conn := range toClose {
		wg.Go(func() { conn.Close() })
	}
	wg.Wait()
}
