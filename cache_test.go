package dns

import (
	"context"
	"testing"
)

func TestCachingResolver(t *testing.T) {
	resolver := NewCachingResolver(nil)
	ips, err := resolver.LookupIPAddr(context.TODO(), "one.one.one.one")
	if err != nil {
		t.Error(err)
	} else {
		t.Log(ips)
	}
}
