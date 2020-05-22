package dns

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// NewHTTPSResolver creates a DNS over HTTPS resolver.
func NewHTTPSResolver(addrs ...string) *net.Resolver {
	for i, a := range addrs {
		ip := net.ParseIP(a)
		if ip != nil && ip.To4() == nil {
			addrs[i] = "[" + a + "]"
		}
	}

	var server uint32
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (conn net.Conn, err error) {
			s := atomic.LoadUint32(&server)
			return &httpConn{
				server:    addrs[s],
				badServer: func() { atomic.CompareAndSwapUint32(&server, s, (s+1)%uint32(len(addrs))) },
			}, nil
		},
	}
}

type httpConn struct {
	sync.Mutex

	server    string
	badServer func()

	queue []string

	cancel   context.CancelFunc
	deadline time.Time
}

// Write implements net.Conn.
func (c *httpConn) Write(b []byte) (n int, err error) {
	c.enqueue(string(b))
	return len(b), nil
}

// Read implements net.Conn.
func (c *httpConn) Read(b []byte) (n int, err error) {
	// deque message
	msg := c.dequeue()
	if msg == "" {
		err = io.EOF
		return
	}

	url := url.URL{
		Scheme: "https",
		Host:   c.server,
		Path:   "/dns-query",
	}

	// prepare request
	req, err := http.NewRequestWithContext(c.context(),
		http.MethodPost, url.String(), strings.NewReader(msg))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/dns-message")

	// send request
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.badServer()
		return
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		err = errors.New(http.StatusText(res.StatusCode))
		return
	}

	// read response
	for n < len(b) && err == nil {
		var i int
		i, err = res.Body.Read(b[n:])
		n += i
	}
	if err == io.EOF {
		err = nil
	}
	return
}

// Close implements net.Conn, net.PacketConn.
func (c *httpConn) Close() error {
	c.Lock()
	cancel := c.cancel
	c.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// LocalAddr implements net.Conn, net.PacketConn.
func (c *httpConn) LocalAddr() net.Addr {
	return nil
}

// RemoteAddr implements net.Conn.
func (c *httpConn) RemoteAddr() net.Addr {
	return nil
}

// SetDeadline implements net.Conn, net.PacketConn.
func (c *httpConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}

// SetReadDeadline implements net.Conn, net.PacketConn.
func (c *httpConn) SetReadDeadline(t time.Time) error {
	c.Lock()
	defer c.Unlock()
	c.deadline = t
	return nil
}

// SetWriteDeadline implements net.Conn, net.PacketConn.
func (c *httpConn) SetWriteDeadline(t time.Time) error {
	// writes do not timeout
	return nil
}

// ReadFrom implements net.PacketConn.
func (c *httpConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	// on a connected PacketConn, ReadFrom does a Read from the RemoteAddr
	addr = c.RemoteAddr()
	n, err = c.Read(p)
	return
}

// WriteTo implements net.PacketConn.
func (c *httpConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// on a connected PacketConn, WriteTo errors
	return 0, net.ErrWriteToConnected
}

func (c *httpConn) enqueue(b string) {
	c.Lock()
	defer c.Unlock()
	c.queue = append(c.queue, b)
}

func (c *httpConn) dequeue() (msg string) {
	c.Lock()
	defer c.Unlock()
	if len(c.queue) > 0 {
		msg = c.queue[0]
		c.queue = c.queue[1:]
	}
	return
}

func (c *httpConn) context() (ctx context.Context) {
	c.Lock()
	defer c.Unlock()
	ctx, c.cancel = context.WithDeadline(context.Background(), c.deadline)
	return
}
