package dns_test

import (
	"context"
	"fmt"

	"github.com/ncruces/go-dns"
)

func ExampleNewHTTPSResolver() {
	resolver := dns.NewHTTPSResolver(
		"1.1.1.1", "2606:4700:4700::1111",
		"1.0.0.1", "2606:4700:4700::1001")

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
