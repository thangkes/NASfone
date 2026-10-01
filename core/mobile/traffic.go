package mobile

import (
	"net"
	"sync"
	"sync/atomic"
)

// traffic counts open connections and bytes moved across all listeners.
type traffic struct {
	open     atomic.Int64
	bytesIn  atomic.Int64 // client -> server (uploads)
	bytesOut atomic.Int64 // server -> client (downloads)
}

// wrap returns a listener whose connections are counted in t.
func (t *traffic) wrap(ln net.Listener) net.Listener { return &countingListener{Listener: ln, t: t} }

type countingListener struct {
	net.Listener
	t *traffic
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.t.open.Add(1)
	return &countingConn{Conn: c, t: l.t}, nil
}

type countingConn struct {
	net.Conn
	t    *traffic
	once sync.Once
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.t.bytesIn.Add(int64(n))
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.t.bytesOut.Add(int64(n))
	return n, err
}

func (c *countingConn) Close() error {
	c.once.Do(func() { c.t.open.Add(-1) })
	return c.Conn.Close()
}
