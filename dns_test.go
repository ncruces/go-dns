package dns_test

import (
	"net"
	"reflect"
)

func check(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

func checkIPAddrs(got []net.IPAddr, wanted ...string) bool {
	if len(got) != len(wanted) {
		return false
	}

	var a = make(map[string]int, len(got))
	for _, ip := range got {
		a[ip.String()] = a[ip.String()] + 1
	}

	var b = make(map[string]int, len(wanted))
	for _, ip := range wanted {
		b[ip] = b[ip] + 1
	}

	return check(a, b)
}
