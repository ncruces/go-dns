package dns

import (
	"net"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	net.DefaultResolver = NewCachingResolver(nil)
	os.Exit(m.Run())
}

func TestCachingResolver(t *testing.T) {
	if ips, err := net.LookupIP("one.one.one.one"); err != nil {
		t.Error(err)
	} else {
		t.Log(ips)
	}
}
