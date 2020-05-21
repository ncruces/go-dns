package dns

import (
	"context"
	"io"
	"net"
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
		ctx, cancel := context.WithCancel(ctx)
		return &cachingConn{
			cache:   &cache,
			ctx:     ctx,
			cancel:  cancel,
			network: network,
			address: address,
		}, nil
	}
}

type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type cache struct {
	dial dialFunc
}

type cachingConn struct {
	cache   *cache
	ctx     context.Context
	cancel  context.CancelFunc
	network string
	address string
	queue   []string
}

func (c *cachingConn) Write(b []byte) (n int, err error) {
	c.queue = append(c.queue, string(b))
	return len(b), nil
}

func (c *cachingConn) Read(b []byte) (n int, err error) {
	// nothing to read
	if len(c.queue) == 0 {
		return
	}

	// deque message
	msg := c.queue[0]
	c.queue = c.queue[1:]

	// TODO: cache.get

	// dial
	var conn net.Conn
	if c.cache.dial != nil {
		conn, err = c.cache.dial(c.ctx, c.network, c.address)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(c.ctx, c.network, c.address)
	}
	if err != nil {
		return
	}
	defer conn.Close()

	// set deadline
	if t, ok := c.ctx.Deadline(); ok {
		err = conn.SetDeadline(t)
	}
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

func (c *cachingConn) Close() error {
	c.cancel()
	return nil
}

func (c *cachingConn) LocalAddr() net.Addr {
	return nil
}

func (c *cachingConn) RemoteAddr() net.Addr {
	return nil
}

func (c *cachingConn) SetDeadline(t time.Time) error {
	c.ctx, c.cancel = context.WithDeadline(c.ctx, t)
	return nil
}

func (c *cachingConn) SetReadDeadline(t time.Time) error {
	return c.SetDeadline(t)
}

func (c *cachingConn) SetWriteDeadline(t time.Time) error {
	return c.SetDeadline(t)
}

func (c *cachingConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	addr = c.RemoteAddr()
	n, err = c.Read(p)
	return
}

func (c *cachingConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return 0, net.ErrWriteToConnected
}
