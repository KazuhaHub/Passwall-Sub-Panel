//go:build destination_acceptance && linux

package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Owned by one serial worker. Only scratch storage is reused: every call still
// dials a fresh TCP connection, encodes its current packet and checks its nonce.
// The reader never retains a connection after the request returns.
type nativeVLESSWorkerProbe struct {
	reader *bufio.Reader
	body   [1024]byte
}

func (p *nativeVLESSWorkerProbe) request(ctx context.Context, inbound int, address string, port int, uuid, nonce, host string) error {
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(inbound)))
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	header, err := nativeVLESSRequest(uuid, address, port, host)
	if err != nil {
		return err
	}
	if _, err := c.Write(header); err != nil {
		return err
	}
	if p.reader == nil {
		p.reader = bufio.NewReader(c)
	} else {
		p.reader.Reset(c)
	}
	defer p.reader.Reset(nil)
	// Buffer the same stream before its prefix so bytes already returned by a
	// read can also satisfy the HTTP parser, rather than another small syscall.
	var response [2]byte
	if _, err := io.ReadFull(p.reader, response[:]); err != nil {
		return err
	}
	if response[0] != 0 {
		return fmt.Errorf("unexpected VLESS response version")
	}
	if _, err := io.CopyN(io.Discard, p.reader, int64(response[1])); err != nil {
		return err
	}
	responseHTTP, err := http.ReadResponse(p.reader, nil)
	if err != nil {
		return err
	}
	defer responseHTTP.Body.Close()
	// Preserve ReadAll(LimitReader(body, 1024)) semantics, including non-EOF
	// errors on truncated bodies and the original exact 1024-byte boundary.
	used := 0
	for used < len(p.body) {
		n, err := responseHTTP.Body.Read(p.body[used:])
		used += n
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
	}
	if responseHTTP.StatusCode != 200 || string(p.body[:used]) != nonce {
		return fmt.Errorf("unexpected target response")
	}
	return nil
}

func TestNativeGlobalWorkerProbeKeepsFreshConnectionsAndResponseSemantics(t *testing.T) {
	const uuid = "33333333-3333-4333-8333-333333333333"
	const address, host, nonce = "fixture.example.test", "worker.example.test", "nonce-owned-probe"
	valid := func(body string) []byte {
		return append([]byte{0, 0}, []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body))...)
	}
	chunked := append([]byte{0, 0}, []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", len(nonce), nonce))...)
	boundary := strings.Repeat("n", 1024)
	var probe nativeVLESSWorkerProbe
	for _, tc := range []struct {
		name, want string
		reply      []byte
		ok         bool
	}{
		{"plain", nonce, valid(nonce), true},
		{"addons", nonce, append([]byte{0, 2, 9, 8}, valid(nonce)[2:]...), true},
		{"chunked", nonce, chunked, true},
		{"version", nonce, append([]byte{1, 0}, valid(nonce)[2:]...), false},
		{"short_prefix", nonce, []byte{0}, false},
		{"missing_addons", nonce, []byte{0, 3, 1}, false},
		{"malformed_http", nonce, append([]byte{0, 0}, []byte("not HTTP\r\n")...), false},
		{"status", nonce, append([]byte{0, 0}, []byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")...), false},
		{"wrong_nonce", nonce, valid("different"), false},
		{"truncated_body", nonce, append([]byte{0, 0}, []byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\n"+nonce)...), false},
		{"truncated_chunk", nonce, chunked[:len(chunked)-7], false},
		{"oversize_body", nonce, valid(nonce + boundary), false},
		{"boundary", boundary, valid(boundary), true},
		{"original_limit", boundary, valid(boundary + "extra"), true},
		{"empty", "", valid(""), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Follow every response (including errors) with a fresh valid one on
			// the same worker, so buffered bytes cannot carry success forward.
			for round, response := range []struct {
				wire []byte
				want string
				ok   bool
			}{{tc.reply, tc.want, tc.ok}, {valid(nonce), nonce, true}} {
				currentUUID, currentAddress, currentHost := uuid, address, host
				if round == 1 {
					currentUUID, currentAddress, currentHost = "44444444-4444-4444-8444-444444444444", "2001:db8::1", "next-worker.example.test"
				}
				packet, err := nativeVLESSRequest(currentUUID, currentAddress, 12345, currentHost)
				nativeMust(t, err, "encode current independent worker wire reference")
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				nativeMust(t, err, "bind owned worker comparison endpoint")
				t.Cleanup(func() { _ = listener.Close() })
				nativeMust(t, listener.(*net.TCPListener).SetDeadline(time.Now().Add(10*time.Second)), "bound worker comparison accepts")
				var accepted atomic.Int64
				done := make(chan error, 1)
				go func() {
					for range 2 {
						c, err := listener.Accept()
						if err != nil {
							done <- err
							return
						}
						accepted.Add(1)
						_ = c.SetDeadline(time.Now().Add(5 * time.Second))
						actual := make([]byte, len(packet))
						_, err = io.ReadFull(c, actual)
						if err == nil && !bytes.Equal(actual, packet) {
							err = fmt.Errorf("worker changed the actual current packet")
						}
						if err == nil {
							_, err = c.Write(response.wire)
						}
						_ = c.Close()
						if err != nil {
							done <- err
							return
						}
					}
					done <- nil
				}()
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				port := listener.Addr().(*net.TCPAddr).Port
				before := nativeVLESSHost(ctx, port, currentAddress, 12345, currentUUID, response.want, currentHost)
				after := probe.request(ctx, port, currentAddress, 12345, currentUUID, response.want, currentHost)
				cancel()
				nativeMust(t, <-done, "join both owned fresh TCP comparisons")
				_ = listener.Close()
				if (before == nil) != response.ok || (after == nil) != response.ok || nativeRequestFailureIndex(before) != nativeRequestFailureIndex(after) || accepted.Load() != 2 || probe.reader == nil || probe.reader.Buffered() != 0 {
					t.Fatal("worker reuse changed connections, response acceptance, error classes or buffered ownership")
				}
			}
		})
	}
}
