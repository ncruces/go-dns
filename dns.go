// Package dns provides net.Resolver instances that can augment/replace the
// net.DefaultResolver.
//
// To replace the net.DefaultResolver with a caching DNS over HTTPS resolver
// using 1.1.1.1 as the name server:
//
//	net.DefaultResolver = dns.NewCachingResolver(dns.NewHTTPSResolver(
//		"1.1.1.1", "2606:4700:4700::1111",
//		"1.0.0.1", "2606:4700:4700::1001"))
package dns
