package dns

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

func NewCachingResolver(parent *net.Resolver) *net.Resolver {
	if parent == nil {
		parent = net.DefaultResolver
	}

	return &net.Resolver{
		PreferGo:     true,
		StrictErrors: parent.StrictErrors,
		Dial:         NewCachingDialer(parent.Dial),
	}
}

func NewCachingDialer(parent dialFunc) dialFunc {
	var cache cache
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return &cachingConn{
			cache:   &cache,
			network: network,
			address: address,
		}, nil
	}
}

type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type cache struct {
	dial dialFunc
}

// cachingConn implements net.Conn, net.PacketConn, and net.Addr.
type cachingConn struct {
	sync.Mutex

	cache   *cache
	network string
	address string

	queue []string

	cancel   context.CancelFunc
	closer   io.Closer
	deadline time.Time
}

// Write implements net.Conn.
func (c *cachingConn) Write(b []byte) (n int, err error) {
	c.enqueue(string(b))
	return len(b), nil
}

// Read implements net.Conn.
func (c *cachingConn) Read(b []byte) (n int, err error) {
	msg := c.dequeue()
	if msg == "" {
		return
	}

	// TODO: cache.get

	// dial
	var conn net.Conn
	dialCtx := c.dialClx()
	if c.cache.dial != nil {
		conn, err = c.cache.dial(dialCtx, c.network, c.address)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(dialCtx, c.network, c.address)
	}
	if err != nil {
		return
	}
	defer conn.Close()

	// set deadline
	err = c.setDeadline(conn)
	if err != nil {
		return
	}

	// prepare request
	var mm []byte
	if _, ok := conn.(net.PacketConn); ok {
		mm = []byte(msg)
	} else {
		mm = make([]byte, len(msg)+2)
		mm[0] = byte(len(msg) >> 8)
		mm[1] = byte(len(msg))
		copy(mm[2:], msg)
	}

	// send request
	nn, err := conn.Write(mm)
	if err != nil {
		return
	}
	if nn != len(mm) {
		err = io.ErrShortWrite
		return
	}

	// read response
	if _, ok := conn.(net.PacketConn); ok {
		n, err = conn.Read(b)
	} else {
		var sz [2]byte
		_, err = io.ReadFull(conn, sz[:])
		if err != nil {
			return
		}

		size := int(sz[0])<<8 | int(sz[1])
		if len(b) < size {
			err = io.ErrShortBuffer
			return
		}
		n, err = io.ReadFull(conn, b[:size])
	}
	if err != nil {
		return
	}

	// TODO: cache.put
	return
}

// Close implements net.Conn, net.PacketConn.
func (c *cachingConn) Close() error {
	c.Lock()
	cancel := c.cancel
	closer := c.closer
	c.cancel = nil
	c.closer = nil
	c.Unlock()

	if cancel != nil {
		cancel()
	}
	if closer != nil {
		return closer.Close()
	}
	return nil
}

// LocalAddr implements net.Conn, net.PacketConn.
func (c *cachingConn) LocalAddr() net.Addr {
	return nil
}

// RemoteAddr implements net.Conn.
func (c *cachingConn) RemoteAddr() net.Addr {
	return c
}

// SetDeadline implements net.Conn, net.PacketConn.
func (c *cachingConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}

// SetReadDeadline implements net.Conn, net.PacketConn.
func (c *cachingConn) SetReadDeadline(t time.Time) error {
	c.Lock()
	defer c.Unlock()
	c.deadline = t
	return nil
}

// SetWriteDeadline implements net.Conn, net.PacketConn.
// Our writes do not timeout.
func (c *cachingConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// ReadFrom implements net.PacketConn.
// On a connected PacketConn, ReadFrom does a Read from the RemoteAddr.
func (c *cachingConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	addr = c.RemoteAddr()
	n, err = c.Read(p)
	return
}

// WriteTo implements net.PacketConn.
// On a connected PacketConn, WriteTo is an error.
func (c *cachingConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return 0, net.ErrWriteToConnected
}

// Network implements net.Addr.
func (c *cachingConn) Network() string {
	return c.network
}

// String implements net.Addr.
func (c *cachingConn) String() string {
	return c.address
}

func (c *cachingConn) enqueue(b string) {
	c.Lock()
	defer c.Unlock()
	c.queue = append(c.queue, b)
}

func (c *cachingConn) dequeue() (msg string) {
	c.Lock()
	defer c.Unlock()
	if len(c.queue) > 0 {
		msg = c.queue[0]
		c.queue = c.queue[1:]
	}
	return
}

func (c *cachingConn) dialClx() (ctx context.Context) {
	c.Lock()
	defer c.Unlock()
	ctx, c.cancel = context.WithDeadline(context.Background(), c.deadline)
	return
}

func (c *cachingConn) setDeadline(conn net.Conn) error {
	c.Lock()
	defer c.Unlock()
	c.closer = conn
	return conn.SetDeadline(c.deadline)
}
