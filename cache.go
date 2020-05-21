package dns

import (
	"context"
	"io"
	"math"
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
	var cache = cache{dial: parent}
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
	sync.RWMutex
	dial    dialFunc
	entries map[string]cacheEntry
}

type cacheEntry struct {
	deadline time.Time
	value    string
}

func (c *cache) put(req string, res string) {
	// ignore invalid/unmatched messages
	if len(req) < 12 || len(res) < 12 {
		return
	}
	if req[2] >= 0x7f || res[2] < 0x7f {
		return
	}
	if req[0] != res[0] || req[1] != res[1] {
		return
	}

	// ignore uncacheable/unparseable answers
	ttl := getTTL(res)
	if ttl <= 0 {
		return
	}

	c.Lock()
	defer c.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]cacheEntry)
	}

	// do some cache evition
	var tested, evicted int
	for k, e := range c.entries {
		if time.Until(e.deadline) <= 0 {
			// delete expired entry
			delete(c.entries, k)
			evicted++
		}
		tested++

		if tested < 8 {
			continue
		}
		if evicted == 0 && len(c.entries) >= 1000 {
			// delete at least one entry
			delete(c.entries, k)
		}
		break
	}

	// remove message IDs
	c.entries[req[2:]] = cacheEntry{
		deadline: time.Now().Add(ttl),
		value:    res[2:],
	}
}

func (c *cache) get(req string) (res string) {
	// ignore invalid messages
	if len(req) < 12 {
		return
	}
	if req[2] >= 0x7f {
		return
	}

	c.RLock()
	defer c.RUnlock()

	if c.entries == nil {
		return
	}

	// remove message ID
	entry, ok := c.entries[req[2:]]
	if ok && time.Until(entry.deadline) > 0 {
		// prepend correct ID
		return req[:2] + entry.value
	}
	return
}

func getTTL(msg string) time.Duration {
	ttl := math.MaxInt32

	qdcount := getUint16(msg[4:])
	ancount := getUint16(msg[6:])
	nscount := getUint16(msg[8:])
	arcount := getUint16(msg[10:])
	rdcount := ancount + nscount + arcount

	msg = msg[12:] // skip header

	// skip questions
	for i := 0; i < qdcount; i++ {
		name := getNameLen(msg)
		if name < 0 || name+4 > len(msg) {
			return -1
		}
		msg = msg[name+4:]
	}

	// parse records
	for i := 0; i < rdcount; i++ {
		name := getNameLen(msg)
		if name < 0 || name+10 > len(msg) {
			return -1
		}
		rttl := getUint32(msg[name+4:])
		rlen := getUint16(msg[name+8:])
		if name+10+rlen > len(msg) {
			return -1
		}
		if rttl < ttl {
			ttl = rttl
		}
		msg = msg[name+10+rlen:]
	}

	return time.Duration(ttl) * time.Second
}

func getNameLen(msg string) int {
	i := 0
	for i < len(msg) {
		if msg[i] == 0 {
			// end of name
			i += 1
			break
		}
		if msg[i] >= 0xc0 {
			// compressed name
			i += 2
			break
		}
		if msg[i] >= 0x40 {
			// reserved
			return -1
		}
		i += int(msg[i] + 1)
	}
	return i
}

func getUint16(s string) int {
	return int(s[1]) | int(s[0])<<8
}

func getUint32(s string) int {
	return int(s[3]) | int(s[2])<<8 | int(s[1])<<16 | int(s[0])<<24
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
	// deque message
	req := c.dequeue()
	if req == "" {
		err = io.EOF
		return
	}

	// check cache
	if res := c.cache.get(req); res != "" {
		if len(b) < len(res) {
			err = io.ErrShortBuffer
		} else {
			n = copy(b, res)
		}
		return
	}

	// dial connection
	var conn net.Conn
	dialCtx := c.dialContext()
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
		mm = []byte(req)
	} else {
		mm = make([]byte, len(req)+2)
		mm[0] = byte(len(req) >> 8)
		mm[1] = byte(len(req))
		copy(mm[2:], req)
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

	// cache response
	c.cache.put(req, string(b[:n]))
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
func (c *cachingConn) SetWriteDeadline(t time.Time) error {
	// writes do not timeout
	return nil
}

// ReadFrom implements net.PacketConn.
func (c *cachingConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	// on a connected PacketConn, ReadFrom does a Read from the RemoteAddr
	addr = c.RemoteAddr()
	n, err = c.Read(p)
	return
}

// WriteTo implements net.PacketConn.
func (c *cachingConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// on a connected PacketConn, WriteTo errors
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

func (c *cachingConn) dialContext() (ctx context.Context) {
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
