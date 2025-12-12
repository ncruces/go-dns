package dns_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/ncruces/go-dns"
)

func ExampleNewCachingResolver() {
	resolver := dns.NewCachingResolver(nil)

	ips, _ := resolver.LookupIPAddr(context.TODO(), "one.one.one.one")
	for _, ip := range ips {
		fmt.Println(ip.String())
	}

	// Unordered output:
	// 1.1.1.1
	// 1.0.0.1
	// 2606:4700:4700::1111
	// 2606:4700:4700::1001
}

func TestNewCachingResolver(t *testing.T) {
	// Prime recursive resolver cache.
	e, err := net.LookupIP("nxdomain.test")
	if err == nil {
		t.Errorf("LookupIPAddr('nxdomain.test') = %v", e)
	}

	r := dns.NewCachingResolver(nil)
	measure := func() time.Duration {
		start := time.Now()
		r.LookupIPAddr(t.Context(), "nxdomain.test")
		return time.Since(start)
	}

	uncached, cached := measure(), measure()
	if uncached > cached*5 { // Expect a big difference.
		t.Logf("uncached %v, cached %v", uncached, cached)
	} else {
		t.Errorf("uncached %v, cached %v", uncached, cached)
	}
}

func TestNegativeCache(t *testing.T) {
	// Prime recursive resolver cache.
	e, err := net.LookupIP("nxdomain.test")
	if err == nil {
		t.Errorf("LookupIPAddr('nxdomain.test') = %v", e)
	}

	r := dns.NewCachingResolver(nil, dns.NegativeCache(false))
	measure := func() time.Duration {
		start := time.Now()
		r.LookupIPAddr(t.Context(), "nxdomain.test")
		return time.Since(start)
	}

	first, second := measure(), measure()
	if first/10 < second && second < first*10 { // Do not expect huge differences.
		t.Logf("first %v, second %v", first, second)
	} else {
		t.Errorf("first %v, second %v", first, second)
	}
}
