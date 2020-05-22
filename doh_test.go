package dns

import (
	"context"
	"fmt"
)

func ExampleNewHTTPSResolver() {
	resolver := NewHTTPSResolver(
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
