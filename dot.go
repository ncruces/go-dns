package dns

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"sync/atomic"
)

// NewTLSResolver creates a DNS over TLS resolver.
// The server can be an IP address, a host name, or a network address of the form "host:port".
func NewTLSResolver(server string, options ...TLSOption) (*net.Resolver, error) {
	// look for a custom port
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		port = "853"
	} else {
		server = host
	}

	// apply options
	var opts tlsOpts
	for _, o := range options {
		o.apply(&opts)
	}

	// resolve server network addresses
	if len(opts.addrs) == 0 {
		ips, err := net.LookupIP(server)
		if err != nil {
			log.Println(server)
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
	}
	if opts.config.ServerName == "" {
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

// A TLSOption customizes the TLS resolver.
type TLSOption interface {
	apply(*tlsOpts)
}

type tlsOpts struct {
	config    *tls.Config
	addrs     []string
	cache     bool
	cacheOpts []CacheOption
}

type (
	tlsConfigOption tls.Config
	tlsAddresses    []string
	tlsCache        []CacheOption
)

func (o *tlsConfigOption) apply(t *tlsOpts) { t.config = (*tls.Config)(o) }
func (o tlsAddresses) apply(t *tlsOpts)     { t.addrs = ([]string)(o) }
func (o tlsCache) apply(t *tlsOpts)         { t.cache = true; t.cacheOpts = ([]CacheOption)(o) }

// TLSConfig sets the tls.Config used by the resolver.
func TLSConfig(config *tls.Config) TLSOption { return (*tlsConfigOption)(config) }

// TLSAddresses sets the network addresses of the resolver.
// These should be IP addresses, or network addresses of the form "IP:port".
func TLSAddresses(addresses ...string) TLSOption { return tlsAddresses(addresses) }

// TLSCache adds caching to the resolver, with the given options.
func TLSCache(options ...CacheOption) TLSOption { return tlsCache(options) }
