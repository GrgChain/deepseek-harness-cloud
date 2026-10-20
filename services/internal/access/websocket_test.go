package access

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func frame(op byte, masked bool, payload []byte) []byte {
	out := []byte{op, byte(len(payload))}
	if masked {
		out[1] |= 0x80
		out = append(out, 1, 2, 3, 4)
	}
	for i, b := range payload {
		if masked {
			b ^= byte(i%4 + 1)
		}
		out = append(out, b)
	}
	return out
}
func TestWebsocketFramesBoundMessagesAndExcludeKeepaliveActivity(t *testing.T) {
	for _, masked := range []bool{false, true} {
		var activity atomic.Bool
		control := frame(0x89, masked, []byte("ping"))
		reader := newFrameReader(bytes.NewReader(control), masked, &activity)
		if copied, err := io.ReadAll(reader); err != nil || !bytes.Equal(copied, control) || activity.Load() {
			t.Fatal("control frame altered bytes or refreshed idle deadline")
		}
		business := frame(0x82, masked, []byte("message"))
		reader = newFrameReader(bytes.NewReader(business), masked, &activity)
		if copied, err := io.ReadAll(reader); err != nil || !bytes.Equal(copied, business) || !activity.Load() {
			t.Fatal("business activity or frame forwarding lost")
		}
	}
	for name, raw := range map[string][]byte{
		"unmasked-client": frame(0x81, false, []byte("data")), "compressed": frame(0xc1, true, []byte("data")), "orphan-continuation": frame(0x80, true, []byte("data")), "fragmented-control": frame(0x09, true, []byte("data")), "oversized": append([]byte{0x82, 0xff}, func() []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, 10<<20+1); return b }()...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := io.ReadAll(newFrameReader(bytes.NewReader(raw), true, nil)); err == nil {
				t.Fatal("invalid or oversized message accepted")
			}
		})
	}
	// A sequence of individually bounded fragments must also remain bounded.
	header := func(op byte, n uint64) []byte {
		h := make([]byte, 10)
		h[0] = op
		h[1] = 127
		binary.BigEndian.PutUint64(h[2:], n)
		return h
	}
	raw := io.MultiReader(bytes.NewReader(header(2, 6<<20)), io.LimitReader(zeroReader{}, 6<<20), bytes.NewReader(header(0x80, 6<<20)))
	if _, err := io.Copy(io.Discard, newFrameReader(raw, false, nil)); err == nil {
		t.Fatal("fragmented message bypassed total bound")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type socketService struct {
	*browserService
	activity chan bool
	deny     atomic.Bool
	changed  atomic.Bool
}

func (s *socketService) CheckSession(ctx context.Context, host, secret string, active bool) (Authorization, error) {
	select {
	case s.activity <- active:
	default:
	}
	if s.deny.Load() {
		return Authorization{}, unavailable()
	}
	target := s.target
	if s.changed.Load() {
		target.Generation = webapi.ID()
	}
	return Authorization{Target: target, ExpiresAt: time.Now().Add(time.Minute), Cookie: append([]byte(nil), s.cookie...)}, nil
}
func TestWebsocketHasCurrentAuthorizationAndClosesOnLossOrShutdown(t *testing.T) {
	for _, mode := range []string{"dependency-loss", "generation-change", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
			id := webapi.ID()
			host := rules.Host(id)
			svc := &socketService{browserService: &browserService{target: Target{InstanceID: id, Host: host, IP: "172.18.0.5", Generation: webapi.ID()}, cookie: []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")}, activity: make(chan bool, 20)}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != rules.authority(host) || r.Header.Get("Origin") != rules.URL(host) || r.Header.Get("Cookie") != string(svc.cookie) || r.Header.Get("Sec-WebSocket-Extensions") != "" {
					t.Error("native WebSocket identity or compression contract violated")
				}
				conn, buffer, err := http.NewResponseController(w).Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
				buffer.Flush()
				for {
					header := make([]byte, 6)
					if _, err = io.ReadFull(buffer, header); err != nil {
						return
					}
					n := int(header[1] & 127)
					if n > 125 {
						return
					}
					payload := make([]byte, n)
					if _, err = io.ReadFull(buffer, payload); err != nil {
						return
					}
					for i := range payload {
						payload[i] ^= header[2+i%4]
					}
					op := header[0]
					if op&15 == 9 {
						op = 0x8a
					}
					if _, err = conn.Write(frame(op, false, payload)); err != nil {
						return
					}
				}
			}))
			defer upstream.Close()
			g := NewGateway(rules, webapi.ID(), svc, nil)
			g.checkInterval = 50 * time.Millisecond
			g.transport.DialContext = testDial(upstream)
			defer g.Close()
			server := httptest.NewServer(g)
			defer server.Close()
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			fmt.Fprintf(conn, "GET /api/remote.mux HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Extensions: permessage-deflate\r\nCookie: %s=%s\r\n\r\n", rules.authority(host), rules.URL(host), base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16)), SessionCookie, strings.Repeat("s", 43))
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, nil)
			if err != nil || response.StatusCode != 101 {
				t.Fatal("WebSocket handshake failed")
			}
			if active := <-svc.activity; !active {
				t.Fatal("initial user request was not activity")
			}
			if active := <-svc.activity; active {
				t.Fatal("periodic connection check extended idle time")
			}
			conn.Write(frame(0x89, true, []byte("ping")))
			reply := make([]byte, 6)
			if _, err = io.ReadFull(reader, reply); err != nil {
				t.Fatal(err)
			}
			if active := <-svc.activity; active {
				t.Fatal("ping/pong extended idle time")
			}
			conn.Write(frame(0x82, true, []byte("work")))
			if _, err = io.ReadFull(reader, reply); err != nil {
				t.Fatal(err)
			}
			if active := <-svc.activity; !active {
				t.Fatal("business activity failed to refresh idle time")
			}
			switch mode {
			case "dependency-loss":
				svc.deny.Store(true)
			case "generation-change":
				svc.changed.Store(true)
			case "shutdown":
				g.Close()
			}
			started := time.Now()
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, err = reader.ReadByte()
			if err == nil || time.Since(started) > 400*time.Millisecond {
				t.Fatal("lost authorization or shutdown did not close the active socket")
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("socket survived reauthorization deadline")
			}
		})
	}
}
func TestWebsocketPerInstanceConnectionLimit(t *testing.T) {
	g := NewGateway(Rules{}, "", nil, nil)
	defer g.Close()
	var releases []func()
	for range 20 {
		release, ok := g.socket("same-instance")
		if !ok {
			t.Fatal("early connection refusal")
		}
		releases = append(releases, release)
	}
	if _, ok := g.socket("same-instance"); ok {
		t.Fatal("connection limit bypassed")
	}
	for _, release := range releases {
		release()
	}
	if release, ok := g.socket("same-instance"); !ok {
		t.Fatal("connection slot leaked")
	} else {
		release()
	}
	g.Close()
	if _, ok := g.socket("same-instance"); ok {
		t.Fatal("shutdown accepted a new connection")
	}
}
