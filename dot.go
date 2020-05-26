package dns

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
)

// NewDoTResolver creates a DNS over TLS resolver.
// The server can be an IP address, a host name, or a network address of the form "host:port".
func NewDoTResolver(server string, options ...DoTOption) (*net.Resolver, error) {
	// look for a custom port
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		port = "853"
	} else {
		server = host
	}

	// apply options
	var opts dotOpts
	for _, o := range options {
		o.apply(&opts)
	}

	// resolve server network addresses
	if len(opts.addrs) == 0 {
		ips, err := net.LookupIP(server)
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

	// setup TLS config
	if opts.config == nil {
		opts.config = &tls.Config{
			ServerName:         server,
			ClientSessionCache: tls.NewLRUClientSessionCache(len(opts.addrs)),
		}
	} else {
		opts.config = opts.config.Clone()
		opts.config.ServerName = server
	}

	// create the resolver
	var resolver = net.Resolver{
		PreferGo:     true,
		StrictErrors: true,
	}

	// setup dialer
	var index uint32
	resolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		s := atomic.LoadUint32(&index)
		conn, err := d.DialContext(ctx, "tcp", opts.addrs[s])
		if err != nil {
			atomic.CompareAndSwapUint32(&index, s, (s+1)%uint32(len(opts.addrs)))
			return nil, err
		}
		return tls.Client(conn, opts.config), nil
	}

	// setup caching
	if opts.cache {
		resolver.Dial = NewCachingDialer(resolver.Dial, opts.cacheOpts...)
	}

	return &resolver, nil
}

// A DoTOption customizes the DNS over TLS resolver.
type DoTOption interface {
	apply(*dotOpts)
}

type dotOpts struct {
	config    *tls.Config
	addrs     []string
	cache     bool
	cacheOpts []CacheOption
}

type (
	dotConfig    tls.Config
	dotAddresses []string
	dotCache     []CacheOption
)

func (o *dotConfig) apply(t *dotOpts)   { t.config = (*tls.Config)(o) }
func (o dotAddresses) apply(t *dotOpts) { t.addrs = ([]string)(o) }
func (o dotCache) apply(t *dotOpts)     { t.cache = true; t.cacheOpts = ([]CacheOption)(o) }

// DoTConfig sets the tls.Config used by the resolver.
func DoTConfig(config *tls.Config) DoTOption { return (*dotConfig)(config) }

// DoTAddresses sets the network addresses of the resolver.
// These should be IP addresses, or network addresses of the form "IP:port".
func DoTAddresses(addresses ...string) DoTOption { return dotAddresses(addresses) }

// DoTCache adds caching to the resolver, with the given options.
func DoTCache(options ...CacheOption) DoTOption { return dotCache(options) }
