// Package dns provides net.Resolver instances that can augment/replace the
// net.DefaultResolver.
//
// To replace the net.DefaultResolver with a caching resolver:
//	net.DefaultResolver = NewCachingResolver(nil)
package dns
