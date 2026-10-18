package main

// Minimal RFC 6455 client. golang.org/x/net/websocket always sends an Origin
// header, which Chrome 111+ rejects unless --remote-allow-origins is set, so
// this client sends none.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

type wsConn struct {
	c net.Conn
	r *bufio.Reader
}

func dialWS(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported websocket scheme %q", u.Scheme)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "80")
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		nc.SetDeadline(dl)
	}
	keyRaw := make([]byte, 16)
	rand.Read(keyRaw)
	key := base64.StdEncoding.EncodeToString(keyRaw)
	fmt.Fprintf(nc, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", u.RequestURI(), u.Host, key)

	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		nc.Close()
		return nil, err
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.StatusCode != http.StatusSwitchingProtocols ||
		resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		nc.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", resp.Status)
	}
	return &wsConn{c: nc, r: br}, nil
}

func (w *wsConn) Close() error { return w.c.Close() }

func (w *wsConn) sendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.writeFrame(0x1, b)
}

// writeFrame sends a single masked frame (clients must mask).
func (w *wsConn) writeFrame(op byte, p []byte) error {
	h := []byte{0x80 | op}
	switch n := len(p); {
	case n < 126:
		h = append(h, 0x80|byte(n))
	case n <= 0xffff:
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	default:
		h = binary.BigEndian.AppendUint64(append(h, 0x80|127), uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	h = append(h, mask...)
	masked := make([]byte, len(p))
	for i := range p {
		masked[i] = p[i] ^ mask[i%4]
	}
	_, err := w.c.Write(append(h, masked...))
	return err
}

// recvJSON reads the next text frame into v. Pings are answered, a close
// frame is io.EOF, fragmented messages are an error.
func (w *wsConn) recvJSON(v any) error {
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.r, h[:]); err != nil {
			return err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > 64<<20 {
			return errors.New("websocket frame too large")
		}
		var mask [4]byte
		masked := h[1]&0x80 != 0
		if masked {
			if _, err := io.ReadFull(w.r, mask[:]); err != nil {
				return err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.r, p); err != nil {
			return err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		switch {
		case op == 0x8:
			return io.EOF
		case op == 0x9:
			if err := w.writeFrame(0xA, p); err != nil {
				return err
			}
		case op == 0xA:
		case !fin || op == 0x0:
			return errors.New("fragmented websocket messages are not supported")
		case op == 0x1:
			return json.Unmarshal(p, v)
		default:
			return fmt.Errorf("unexpected websocket opcode %d", op)
		}
	}
}
