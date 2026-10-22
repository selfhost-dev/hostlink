package httpclient

import (
	"io"
	"net"
	"sync"
	"testing"
)

// blackholeProxy forwards TCP to a backend until stall: from then on the
// connections it already holds swallow every byte both ways, as a stalled
// network path does, while new connections forward normally.
type blackholeProxy struct {
	ln      net.Listener
	backend string

	mu      sync.Mutex
	conns   []*proxiedConn
	dialled int
}

type proxiedConn struct {
	client, server net.Conn
	mu             sync.Mutex
	stalled        bool
}

func newBlackholeProxy(t *testing.T, backend string) *blackholeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &blackholeProxy{ln: ln, backend: backend}
	go p.serve()
	t.Cleanup(p.close)
	return p
}

func (p *blackholeProxy) addr() string { return p.ln.Addr().String() }

func (p *blackholeProxy) serve() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", p.backend)
		if err != nil {
			client.Close()
			continue
		}
		c := &proxiedConn{client: client, server: server}
		p.mu.Lock()
		p.conns = append(p.conns, c)
		p.dialled++
		p.mu.Unlock()
		go c.pipe(server, client)
		go c.pipe(client, server)
	}
}

func (c *proxiedConn) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			c.mu.Lock()
			stalled := c.stalled
			c.mu.Unlock()
			if !stalled {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					return
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				return
			}
			return
		}
	}
}

// stall black-holes every connection open now.
func (p *blackholeProxy) stall() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.mu.Lock()
		c.stalled = true
		c.mu.Unlock()
	}
}

func (p *blackholeProxy) connections() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dialled
}

func (p *blackholeProxy) close() {
	p.ln.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.client.Close()
		c.server.Close()
	}
}
