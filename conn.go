package dns

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type dnsConn struct {
	sync.Mutex

	ibuf bytes.Buffer
	obuf bytes.Buffer

	cancel   context.CancelFunc
	closer   io.Closer
	deadline time.Time
	exchange func(*dnsConn, string) (string, error)
}

// Read implements net.Conn.
func (c *dnsConn) Read(b []byte) (n int, err error) {
	imsg, n, err := c.drainBuffers(b)
	if n != 0 || err != nil {
		return n, err
	}

	omsg, err := c.exchange(c, imsg)
	if err != nil {
		return 0, err
	}

	c.Lock()
	defer c.Unlock()
	c.obuf.WriteByte(byte(len(omsg) >> 8))
	c.obuf.WriteByte(byte(len(omsg)))
	c.obuf.WriteString(omsg)
	return c.obuf.Read(b)
}

// Write implements net.Conn.
func (c *dnsConn) Write(b []byte) (n int, err error) {
	c.Lock()
	defer c.Unlock()
	return c.ibuf.Write(b)
}

// Close implements net.Conn.
func (c *dnsConn) Close() error {
	c.Lock()
	cancel := c.cancel
	closer := c.closer
	c.closer = nil
	c.Unlock()

	if cancel != nil {
		cancel()
	}
	if closer != nil {
		return closer.Close()
	}
	return nil
}

// LocalAddr implements net.Conn.
func (c *dnsConn) LocalAddr() net.Addr {
	return nil
}

// RemoteAddr implements net.Conn.
func (c *dnsConn) RemoteAddr() net.Addr {
	return nil
}

// SetDeadline implements net.Conn.
func (c *dnsConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}

// SetReadDeadline implements net.Conn.
func (c *dnsConn) SetReadDeadline(t time.Time) error {
	c.Lock()
	defer c.Unlock()
	c.deadline = t
	return nil
}

// SetWriteDeadline implements net.Conn.
func (c *dnsConn) SetWriteDeadline(t time.Time) error {
	// writes do not timeout
	return nil
}

func (c *dnsConn) drainBuffers(b []byte) (string, int, error) {
	c.Lock()
	defer c.Unlock()

	// drain the output buffer
	if c.obuf.Len() > 0 {
		n, err := c.obuf.Read(b)
		return "", n, err
	}

	// otherwise, get the next message from the input buffer
	sz := c.ibuf.Next(2)
	if len(sz) < 2 {
		return "", 0, io.ErrUnexpectedEOF
	}

	size := int64(sz[0])<<8 | int64(sz[1])

	var str strings.Builder
	_, err := io.CopyN(&str, &c.ibuf, size)
	if err == io.EOF {
		return "", 0, io.ErrUnexpectedEOF
	}
	if err != nil {
		return "", 0, err
	}
	return str.String(), 0, nil
}

func (c *dnsConn) getContext() (ctx context.Context) {
	c.Lock()
	defer c.Unlock()
	ctx, c.cancel = context.WithDeadline(context.Background(), c.deadline)
	return ctx
}

func (c *dnsConn) setChild(child net.Conn) error {
	c.Lock()
	defer c.Unlock()
	c.closer = child
	return child.SetDeadline(c.deadline)
}

func writeMessage(conn net.Conn, msg string) error {
	var buf []byte
	if _, ok := conn.(net.PacketConn); ok {
		buf = []byte(msg)
	} else {
		buf = make([]byte, len(msg)+2)
		buf[0] = byte(len(msg) >> 8)
		buf[1] = byte(len(msg))
		copy(buf[2:], msg)
	}
	// SHOULD do a single write on TCP (RFC 7766, section 8).
	// MUST do a single write on UDP.
	_, err := conn.Write(buf)
	return err
}

func readMessage(c net.Conn) (string, error) {
	if _, ok := c.(net.PacketConn); ok {
		// RFC 1035 specifies 512 as the maximum message size for DNS over UDP.
		// But accept the UDPv6 minimum of 1232 payload.
		b := make([]byte, 1232)
		n, err := c.Read(b)
		if err != nil {
			return "", err
		}
		return string(b[:n]), nil
	} else {
		var sz [2]byte
		_, err := io.ReadFull(c, sz[:])
		if err != nil {
			return "", err
		}

		size := int64(sz[0])<<8 | int64(sz[1])

		var str strings.Builder
		_, err = io.CopyN(&str, c, size)
		if err == io.EOF {
			return "", io.ErrUnexpectedEOF
		}
		if err != nil {
			return "", err
		}
		return str.String(), nil
	}
}
