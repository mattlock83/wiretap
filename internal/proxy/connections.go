package proxy

import (
	"net"
	"net/http"
	"sync"
)

// Connections includes h2c connections, which net/http hands off to HTTP/2 and
// consequently excludes from Server.Shutdown and Server.Close.
type Connections struct {
	mu          sync.Mutex
	connections map[net.Conn]struct{}
}

func (c *Connections) State(conn net.Conn, state http.ConnState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state == http.StateClosed {
		delete(c.connections, conn)
		return
	}
	if c.connections == nil {
		c.connections = map[net.Conn]struct{}{}
	}
	c.connections[conn] = struct{}{}
}
func (c *Connections) Close() {
	c.mu.Lock()
	connections := c.connections
	c.connections = nil
	c.mu.Unlock()
	for conn := range connections {
		_ = conn.Close()
	}
}
