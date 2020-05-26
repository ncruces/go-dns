package dns_test

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/ncruces/go-dns"
)

func ExampleNewTLSResolver() {
	resolver, err := dns.NewTLSResolver("dns.google")
	if err != nil {
		log.Fatal(err)
	}

	ips, _ := resolver.LookupIPAddr(context.TODO(), "dns.google")
	for _, ip := range ips {
		fmt.Println(ip.String())
	}

	// Unordered output:
	// 8.8.8.8
	// 8.8.4.4
	// 2001:4860:4860::8888
	// 2001:4860:4860::8844
}

func TestNewTLSResolver(t *testing.T) {
	// DNS-over-TLS Public Resolvers
	tests := map[string]struct {
		server string
		opts   []dns.TLSOption
	}{
		"Google": {server: "dns.google"},
		"Quad9":  {server: "dns.quad9.net"},
		"Cloudflare": {
			server: "cloudflare-dns.com",
			opts: []dns.TLSOption{
				dns.TLSAddresses("1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r, err := dns.NewTLSResolver(tc.server, tc.opts...)
			if err != nil {
				t.Fatalf("NewTLSResolver(...) error = %v", err)
				return
			}

			e, err := r.LookupIPAddr(context.TODO(), "nxdomain.test")
			if err == nil {
				t.Errorf("LookupIPAddr('nxdomain.test') = %v", e)
			}

			ips, err := r.LookupIPAddr(context.TODO(), "one.one.one.one")
			if err != nil {
				t.Fatalf("LookupIPAddr('one.one.one.one') error = %v", err)
				return
			}

			if !checkIPAddrs(ips, "1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001") {
				t.Errorf("LookupIPAddr('one.one.one.one') = %v", ips)
			}
		})
	}

	t.Run("Cache", func(t *testing.T) {
		r, err := dns.NewTLSResolver("1.1.1.1", dns.TLSCache())
		if err != nil {
			t.Fatalf("NewTLSResolver(...) error = %v", err)
			return
		}

		a, err := r.LookupIPAddr(context.TODO(), "one.one.one.one")
		if err != nil {
			t.Fatalf("LookupIPAddr('one.one.one.one') error = %v", err)
			return
		}

		b, err := r.LookupIPAddr(context.TODO(), "one.one.one.one")
		if err != nil {
			t.Fatalf("LookupIPAddr('one.one.one.one') error = %v", err)
			return
		}

		if !check(a, b) {
			t.Errorf("LookupIPAddr('one.one.one.one') = %v [wanted %v]", b, a)
		}
	})
}
