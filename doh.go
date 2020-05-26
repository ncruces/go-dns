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

// NewDoHResolver creates a DNS over HTTPS resolver.
func NewDoHResolver(uri string, options ...DoHOption) (*net.Resolver, error) {
	// parse the uri template into a url
	uri, err := parseURITemplate(uri)
	if err != nil {
		return nil, err
	}
	url, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	port := url.Port()
	if port == "" {
		port = url.Scheme
	}

	// apply options
	var opts dohOpts
	for _, o := range options {
		o.apply(&opts)
	}

	// resolve server network addresses
	if len(opts.addrs) == 0 {
		ips, err := net.LookupIP(url.Hostname())
		if err != nil {
			return nil, err
		}
		opts.addrs = make([]string, len(ips))
		for i, ip := range ips {
			opts.addrs[i] = net.JoinHostPort(ip.String(), port)
		}
	} else {
		for i, a := range opts.addrs {
			if net.ParseIP(a) != nil {
				opts.addrs[i] = net.JoinHostPort(a, port)
			}
		}
	}

	// setup the http transport
	if opts.transport == nil {
		opts.transport = &http.Transport{
			MaxIdleConns:        http.DefaultMaxIdleConnsPerHost,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			ForceAttemptHTTP2:   true,
		}
	} else {
		opts.transport = opts.transport.Clone()
	}

	// setup the http client
	client := http.Client{
		Transport: opts.transport,
	}

	// create the resolver
	var resolver = net.Resolver{
		PreferGo:     true,
		StrictErrors: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return &dohConn{uri: uri, client: &client}, nil
		},
	}

	// setup dialer
	var index uint32
	opts.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		s := atomic.LoadUint32(&index)
		conn, err := d.DialContext(ctx, network, opts.addrs[s])
		if err != nil {
			atomic.CompareAndSwapUint32(&index, s, (s+1)%uint32(len(opts.addrs)))
			return nil, err
		}
		return conn, err
	}

	// setup caching
	if opts.cache {
		resolver.Dial = NewCachingDialer(resolver.Dial, opts.cacheOpts...)
	}

	return &resolver, nil
}

// An DoHOption customizes the DNS over HTTPS resolver.
type DoHOption interface {
	apply(*dohOpts)
}

type dohOpts struct {
	transport *http.Transport
	addrs     []string
	cache     bool
	cacheOpts []CacheOption
}

type (
	dohTransport http.Transport
	dohAddresses []string
	dohCache     []CacheOption
)

func (o *dohTransport) apply(t *dohOpts) { t.transport = (*http.Transport)(o) }
func (o dohAddresses) apply(t *dohOpts)  { t.addrs = ([]string)(o) }
func (o dohCache) apply(t *dohOpts)      { t.cache = true; t.cacheOpts = ([]CacheOption)(o) }

// DoHTransport sets the http.Transport used by the resolver.
func DoHTransport(transport *http.Transport) DoHOption { return (*dohTransport)(transport) }

// DoHAddresses sets the network addresses of the resolver.
// These should be IP addresses, or network addresses of the form "IP:port".
func DoHAddresses(addresses ...string) DoHOption { return dohAddresses(addresses) }

// DoHCache adds caching to the resolver, with the given options.
func DoHCache(options ...CacheOption) DoHOption { return dohCache(options) }

type dohConn struct {
	sync.Mutex

	uri    string
	client *http.Client

	queue []string

	cancel   context.CancelFunc
	deadline time.Time
}

// Write implements net.Conn.
func (c *dohConn) Write(b []byte) (n int, err error) {
	c.enqueue(string(b))
	return len(b), nil
}

// Read implements net.Conn.
func (c *dohConn) Read(b []byte) (n int, err error) {
	// deque message
	msg := c.dequeue()
	if msg == "" {
		return 0, io.EOF
	}

	// prepare request
	req, err := http.NewRequestWithContext(c.context(),
		http.MethodPost, c.uri, strings.NewReader(msg))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/dns-message")

	// send request
	res, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, errors.New(http.StatusText(res.StatusCode))
	}

	// read response
	for n < len(b) && err == nil {
		var i int
		i, err = res.Body.Read(b[n:])
		n += i
	}
	if err == io.EOF {
		return n, nil
	}
	if err == nil {
		var bb [1]byte
		if i, _ := res.Body.Read(bb[:]); i > 0 {
			return n, io.ErrShortBuffer
		}
	}
	return n, err
}

// Close implements net.Conn, net.PacketConn.
func (c *dohConn) Close() error {
	c.Lock()
	cancel := c.cancel
	c.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// LocalAddr implements net.Conn, net.PacketConn.
func (c *dohConn) LocalAddr() net.Addr {
	return nil
}

// RemoteAddr implements net.Conn.
func (c *dohConn) RemoteAddr() net.Addr {
	return nil
}

// SetDeadline implements net.Conn, net.PacketConn.
func (c *dohConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}

// SetReadDeadline implements net.Conn, net.PacketConn.
func (c *dohConn) SetReadDeadline(t time.Time) error {
	c.Lock()
	defer c.Unlock()
	c.deadline = t
	return nil
}

// SetWriteDeadline implements net.Conn, net.PacketConn.
func (c *dohConn) SetWriteDeadline(t time.Time) error {
	// writes do not timeout
	return nil
}

// ReadFrom implements net.PacketConn.
func (c *dohConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	// on a connected PacketConn, ReadFrom does a Read from the RemoteAddr
	addr = c.RemoteAddr()
	n, err = c.Read(p)
	return
}

// WriteTo implements net.PacketConn.
func (c *dohConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// on a connected PacketConn, WriteTo errors
	return 0, net.ErrWriteToConnected
}

func (c *dohConn) enqueue(b string) {
	c.Lock()
	defer c.Unlock()
	c.queue = append(c.queue, b)
}

func (c *dohConn) dequeue() (msg string) {
	c.Lock()
	defer c.Unlock()
	if len(c.queue) > 0 {
		msg = c.queue[0]
		c.queue = c.queue[1:]
	}
	return msg
}

func (c *dohConn) context() (ctx context.Context) {
	c.Lock()
	defer c.Unlock()
	ctx, c.cancel = context.WithDeadline(context.Background(), c.deadline)
	return ctx
}

func parseURITemplate(uri string) (string, error) {
	var buf strings.Builder
	var exp bool

	for i := 0; i < len(uri); i++ {
		switch c := uri[i]; c {
		case '{':
			if exp {
				return "", errors.New("uri: invalid syntax")
			}
			exp = true
		case '}':
			if !exp {
				return "", errors.New("uri: invalid syntax")
			}
			exp = false
		default:
			if !exp {
				buf.WriteByte(c)
			}
		}
	}

	return buf.String(), nil
}

func baseTransport() *http.Transport {
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = tr.Clone()
		tr.Proxy = nil
		if tr.MaxIdleConnsPerHost < http.DefaultMaxIdleConnsPerHost {
			tr.MaxIdleConnsPerHost = http.DefaultMaxIdleConnsPerHost
		}
		return tr
	}

	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
