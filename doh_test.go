package dns_test

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/ncruces/go-dns"
)

func ExampleNewDoHResolver() {
	resolver, err := dns.NewDoHResolver("https://dns.google/dns-query{?dns}")
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

func TestNewDoHResolver(t *testing.T) {
	// DNS-over-HTTPS Public Resolvers
	tests := map[string]struct {
		uri  string
		opts []dns.DoHOption
	}{
		"Google":     {uri: "https://dns.google/dns-query"},
		"Quad9":      {uri: "https://dns.quad9.net/dns-query"},
		"Cloudflare": {uri: "https://cloudflare-dns.com/dns-query"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r, err := dns.NewDoHResolver(tc.uri, tc.opts...)
			if err != nil {
				t.Fatalf("NewDoHResolver(...) error = %v", err)
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
		r, err := dns.NewDoHResolver("https://1.1.1.1/dns-query", dns.DoHCache())
		if err != nil {
			t.Fatalf("NewDoHResolver(...) error = %v", err)
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

func TestNewDoH64Resolver(t *testing.T) {
	// DNS64-over-HTTPS Public Resolvers
	tests := map[string]struct {
		uri  string
		opts []dns.DoHOption
	}{
		"Google":     {uri: "https://dns64.dns.google/dns-query"},
		"Cloudflare": {uri: "https://dns64.cloudflare-dns.com/dns-query"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r, err := dns.NewDoHResolver(tc.uri, tc.opts...)
			if err != nil {
				t.Fatalf("NewDoHResolver(...) error = %v", err)
				return
			}

			e, err := r.LookupIPAddr(context.TODO(), "nxdomain.test")
			if err == nil {
				t.Errorf("LookupIPAddr('nxdomain.test') = %v", e)
			}

			ips, err := r.LookupIPAddr(context.TODO(), "ipv4.google.com")
			if err != nil {
				t.Fatalf("LookupIPAddr('ipv4.google.com') error = %v", err)
				return
			}

			for _, ip := range ips {
				if ip.IP.To4() == nil {
					return
				}
			}
			t.Errorf("LookupIPAddr('ipv4.google.com') = %v", ips)
		})
	}
}
