package dns

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
)

// NewTLSResolver creates a DNS over TLS resolver.
func NewTLSResolver(serverName string, addrs ...string) *net.Resolver {
	for i, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		if ip.To4() == nil {
			addrs[i] = "[" + a + "]:853"
		} else {
			addrs[i] = a + ":853"
		}
	}
	cfg := &tls.Config{
		ServerName:         serverName,
		ClientSessionCache: tls.NewLRUClientSessionCache(len(addrs)),
	}

	var server uint32
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			s := atomic.LoadUint32(&server)
			conn, err := d.DialContext(ctx, "tcp", addrs[s])
			if err != nil {
				atomic.CompareAndSwapUint32(&server, s, (s+1)%uint32(len(addrs)))
				return nil, err
			}
			return tls.Client(conn, cfg), nil
		},
	}
}
