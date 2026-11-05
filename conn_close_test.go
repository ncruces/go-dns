package dns_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ncruces/go-dns"
	"golang.org/x/net/dns/dnsmessage"
)

func closeTestResolver(t *testing.T, cached bool, handler http.HandlerFunc) *net.Resolver {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	opts := []dns.DoHOption{dns.DoHAddresses(srv.Listener.Addr().String()), dns.DoHTransport(srv.Client().Transport.(*http.Transport))}
	if cached {
		opts = append(opts, dns.DoHCache())
	}
	r, err := dns.NewDoHResolver(srv.URL+"/dns-query", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func closeTestQuery(t *testing.T, c net.Conn, id uint16) {
	t.Helper()
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("close.test."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	wire, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	wire = append([]byte{byte(len(wire) >> 8), byte(len(wire))}, wire...)
	if _, err := c.Write(wire); err != nil {
		t.Fatal(err)
	}
}

func closeTestReply(c net.Conn) error {
	var size [2]byte
	if _, err := io.ReadFull(c, size[:]); err != nil {
		return err
	}
	_, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(size[:])))
	return err
}

func closeTestAnswer(w http.ResponseWriter, r *http.Request) {
	wire, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	var msg dnsmessage.Message
	if err := msg.Unpack(wire); err != nil || len(msg.Questions) != 1 {
		return
	}
	msg.Header.Response = true
	msg.Answers = []dnsmessage.Resource{{
		Header: dnsmessage.ResourceHeader{Name: msg.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
		Body:   &dnsmessage.AResource{A: [4]byte{127, 0, 0, 42}},
	}}
	wire, err = msg.Pack()
	if err == nil {
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(wire)
	}
}

func TestDoHCloseActiveRequest(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "direct"
		if cached {
			name = "cached"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			canceled := make(chan struct{})
			release := make(chan struct{})
			var requests atomic.Int32
			r := closeTestResolver(t, cached, func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) > 1 {
					closeTestAnswer(w, r)
					return
				}
				// Consume the body so the server can observe a peer disconnect.
				io.Copy(io.Discard, r.Body)
				close(started)
				select {
				case <-r.Context().Done():
					close(canceled)
				case <-release:
				}
			})
			defer close(release)
			c, err := r.Dial(context.Background(), "tcp", "unused:53")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			closeTestQuery(t, c, 1)
			done := make(chan error, 1)
			go func() { done <- closeTestReply(c) }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("DoH request did not start")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("closed request unexpectedly succeeded")
				}
			case <-time.After(time.Second):
				t.Error("Close did not unblock Read")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Error("Close did not cancel the HTTP request")
			}
			// Cancellation belongs to this connection, not the shared client.
			next, err := r.Dial(context.Background(), "tcp", "unused:53")
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			closeTestQuery(t, next, 2)
			if err := closeTestReply(next); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDoHCloseBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	r := closeTestResolver(t, false, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); closeTestAnswer(w, r) })
	c, err := r.Dial(context.Background(), "tcp", "unused:53")
	if err != nil {
		t.Fatal(err)
	}
	closeTestQuery(t, c, 1)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closeTestReply(c); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Read after Close = %v, want net.ErrClosed", err)
	}
	if _, err := c.Write([]byte{0, 0}); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Write after Close = %v, want net.ErrClosed", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("closed connection sent %d requests", n)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDoHReuseAfterDeadline(t *testing.T) {
	var requests atomic.Int32
	r := closeTestResolver(t, false, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); closeTestAnswer(w, r) })
	c, err := r.Dial(context.Background(), "tcp", "unused:53")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	closeTestQuery(t, c, 1)
	if err := closeTestReply(c); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline: got %v", err)
	}
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint16{2, 3} {
		closeTestQuery(t, c, id)
		// Drain with one-byte reads to preserve framed partial-read behavior.
		var size [2]byte
		for i := range size {
			if _, err := io.ReadFull(c, size[i:i+1]); err != nil {
				t.Fatal(err)
			}
		}
		body := make([]byte, binary.BigEndian.Uint16(size[:]))
		for i := range body {
			if _, err := io.ReadFull(c, body[i:i+1]); err != nil {
				t.Fatal(err)
			}
		}
		var msg dnsmessage.Message
		if err := msg.Unpack(body); err != nil {
			t.Fatal(err)
		}
		if msg.ID != id || len(msg.Answers) != 1 {
			t.Fatalf("unexpected response: %+v", msg)
		}
	}
	if n := requests.Load(); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

func TestDoHCloseReadStartupRace(t *testing.T) {
	r := closeTestResolver(t, false, closeTestAnswer)
	for i := 0; i < 50; i++ {
		c, err := r.Dial(context.Background(), "tcp", "unused:53")
		if err != nil {
			t.Fatal(err)
		}
		closeTestQuery(t, c, uint16(i))
		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			<-start
			closeTestReply(c)
			close(done)
		}()
		close(start)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Close raced with Read startup and left it blocked")
		}
		if _, err := c.Write([]byte{0, 0}); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Write after Close = %v", err)
		}
	}
}

func TestDoHDialContextDoesNotCloseEstablishedConnection(t *testing.T) {
	r := closeTestResolver(t, false, closeTestAnswer)
	ctx, cancel := context.WithCancel(context.Background())
	c, err := r.Dial(ctx, "tcp", "unused:53")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Like net.Dialer.DialContext, canceling the dial context after Dial
	// returned does not close an established connection.
	cancel()
	closeTestQuery(t, c, 1)
	if err := closeTestReply(c); err != nil {
		t.Fatal(err)
	}
}
