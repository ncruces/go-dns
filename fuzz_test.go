package dns

import (
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func Fuzz_parsing(f *testing.F) {
	f.Add("", "")
	f.Add("000000000000", "000000000000")
	f.Add("000000000000", "010000000000")
	f.Add("000000000000", "00\x80000000000")
	f.Add("00\x00000000000", "00\x80000000000")
	f.Add("00\x00000000000", "00\x80100000000")

	f.Fuzz(func(t *testing.T, req string, res string) {
		var parser dnsmessage.Parser

		invalid := invalid(req, res)
		hreq, ereq := parser.Start([]byte(req))
		hres, eres := parser.Start([]byte(res))

		if !invalid {
			if ereq != nil || eres != nil { // header size
				t.Fail()
			}
			if hreq.ID != hres.ID { // IDs match
				t.Fail()
			}
			if hreq.Response || !hres.Response { // query, response
				t.Fail()
			}
			if hreq.OpCode != 0 || hres.OpCode != 0 { // standard query
				t.Fail()
			}
			if hreq.Truncated || hres.Truncated { // not truncated
				t.Fail()
			}
			if hres.RCode != 0 && hres.RCode != dnsmessage.RCodeNameError { // no error, or name error
				t.Fail()
			}
			if nameError(res) != (hres.RCode == dnsmessage.RCodeNameError) { // name error
				t.Fail()
			}
		}
	})
}
