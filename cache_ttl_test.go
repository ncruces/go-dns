package dns_test

import (
	"encoding/binary"
	"github.com/ncruces/go-dns"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func ttlExchange(t *testing.T, dial dns.DialFunc, id uint16) dnsmessage.Message {
	t.Helper()
	c, err := dial(t.Context(), "tcp", "unused:53")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	wire, err := (&dnsmessage.Message{Header: dnsmessage.Header{ID: id, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("ttl.test."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}).Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Write(append([]byte{byte(len(wire) >> 8), byte(len(wire))}, wire...)); err != nil {
		t.Fatal(err)
	}
	var size [2]byte
	if _, err = io.ReadFull(c, size[:]); err != nil {
		t.Fatal(err)
	}
	wire = make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err = io.ReadFull(c, wire); err != nil {
		t.Fatal(err)
	}
	var msg dnsmessage.Message
	if err = msg.Unpack(wire); err != nil {
		t.Fatal(err)
	}
	if msg.ID != id {
		t.Fatalf("ID = %d, want %d", msg.ID, id)
	}
	return msg
}

func TestCachedWireTTLs(t *testing.T) {
	for _, tc := range []struct {
		name string
		ttl  uint32
		opts []dns.CacheOption
		miss bool
	}{
		{name: "ordinary", ttl: 60},
		{name: "minimum", ttl: 1, opts: []dns.CacheOption{dns.MinCacheTTL(time.Minute)}},
		{name: "maximum", ttl: 4, opts: []dns.CacheOption{dns.MaxCacheTTL(time.Second)}, miss: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			// Use the exported DoH and caching dialers, real TLS HTTP and DNS framing.
			r := closeTestResolver(t, tc.name == "ordinary", func(w http.ResponseWriter, r *http.Request) {
				wire, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var msg dnsmessage.Message
				if err = msg.Unpack(wire); err != nil {
					t.Error(err)
					return
				}
				requests.Add(1)
				msg.Response = true
				name := msg.Questions[0].Name
				msg.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: tc.ttl}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}}}
				msg.Authorities = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: name, Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET, TTL: 120}, Body: &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns.ttl.test.")}}}
				msg.Additionals = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("ns.ttl.test."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 180}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}}, {Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("."), Type: dnsmessage.TypeOPT, Class: 1232, TTL: 0x8000}, Body: &dnsmessage.OPTResource{}}}
				wire, err = msg.Pack()
				if err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/dns-message")
				w.Write(wire)
			})
			dial := r.Dial
			if tc.name != "ordinary" {
				dial = dns.NewCachingDialer(dial, tc.opts...)
			}
			first := ttlExchange(t, dial, 1)
			if first.Answers[0].Header.TTL != tc.ttl {
				t.Fatalf("initial TTL = %d", first.Answers[0].Header.TTL)
			}
			time.Sleep(1100 * time.Millisecond)
			for _, id := range []uint16{2, 3} {
				got := ttlExchange(t, dial, id)
				if tc.miss {
					if got.Answers[0].Header.TTL != tc.ttl {
						t.Errorf("fresh TTL = %d", got.Answers[0].Header.TTL)
					}
					continue
				}
				age := uint32(120) - got.Authorities[0].Header.TTL
				if age < 1 || age > 59 {
					t.Fatalf("authority TTL = %d, want 61..119", got.Authorities[0].Header.TTL)
				}
				wantTTL := uint32(0)
				if age < tc.ttl {
					wantTTL = tc.ttl - age
				}
				if got.Answers[0].Header.TTL != wantTTL || got.Additionals[0].Header.TTL != 180-age {
					t.Error("records aged inconsistently")
				}
				if got.Additionals[1].Header.TTL != 0x8000 {
					t.Error("EDNS flags changed")
				}
				got.ID = first.ID
				got.Answers[0].Header.TTL = first.Answers[0].Header.TTL
				got.Authorities[0].Header.TTL = first.Authorities[0].Header.TTL
				got.Additionals[0].Header.TTL = first.Additionals[0].Header.TTL
				if !reflect.DeepEqual(got, first) {
					t.Error("response changed beyond ID and TTL")
				}
			}
			want := int32(1)
			if tc.miss {
				want = 2
			}
			if n := requests.Load(); n != want {
				t.Errorf("upstream requests = %d, want %d", n, want)
			}
		})
	}
}
