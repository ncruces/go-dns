// Package dns provides net.Resolver instances that can augment/replace the
// net.DefaultResolver.
//
// To replace the net.DefaultResolver with a caching DNS over HTTPS resolver
// using Google's Public DNS as the name server:
//
//	net.DefaultResolver = dns.NewDoHResolver(
//		"https://dns.google/dns-query",
//		dns.DoHCache())
package dns
