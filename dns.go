// Package dns provides net.Resolver instances implementing caching,
// opportunistic encryption, and DNS over TLS/HTTPS.
//
// To replace the net.DefaultResolver with a caching DNS over HTTPS instance
// using the Google Public DNS resolver:
//
//	net.DefaultResolver = dns.NewDoHResolver(
//		"https://dns.google/dns-query",
//		dns.DoHCache())
package dns

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"
)

// OpportunisticResolver opportunistically tries encrypted DNS over TLS
// using the local resolver.
var OpportunisticResolver = &net.Resolver{
	Dial:     getAnOpportunisticDialerFromDialFunc(nil),
	PreferGo: true,
}

func getAnOpportunisticDialerFromDialFunc(dial DialFunc) DialFunc {
	if dial == nil {
		var d net.Dialer
		dial = d.DialContext
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(address)
		if (port == "53" || port == "domain") && notBadServer(address) {
			deadline, ok := ctx.Deadline()
			if ok && deadline.After(time.Now().Add(2*time.Second)) {
				tlsAddr := net.JoinHostPort(host, "853")
				rawConn, _ := dial(ctx, "tcp", tlsAddr)
				if rawConn != nil {
					tlsConf := tls.Config{InsecureSkipVerify: true}
					return tls.Client(rawConn, &tlsConf), nil
				}
				addBadServer(address)
			}
		}

		return dial(ctx, network, address)
	}
}

var badServers struct {
	sync.Mutex
	next int
	list [4]string
}

func notBadServer(address string) bool {
	badServers.Lock()
	defer badServers.Unlock()
	for _, a := range badServers.list {
		if a == address {
			return false
		}
	}
	return true
}

func addBadServer(address string) {
	badServers.Lock()
	defer badServers.Unlock()
	for _, a := range badServers.list {
		if a == address {
			return
		}
	}
	badServers.list[badServers.next] = address
	badServers.next = (badServers.next + 1) % len(badServers.list)
}
