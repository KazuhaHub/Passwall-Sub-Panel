//go:build destination_acceptance && linux

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

// Fixed categories only: never retain or publish an error string, address,
// request, credential or response body from an actual traffic failure.
var nativeRequestFailureKinds = [...]string{
	"dial_timeout", "write_timeout", "read_timeout", "timeout", "refused",
	"reset", "address_unavailable", "resource_limit", "eof", "short_write", "other",
}

func nativeRequestFailureIndex(err error) int {
	if err == nil {
		return -1
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return 4
	case errors.Is(err, syscall.ECONNRESET):
		return 5
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return 6
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE), errors.Is(err, syscall.ENOBUFS), errors.Is(err, syscall.ENOMEM):
		return 7
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return 8
	case errors.Is(err, io.ErrShortWrite):
		return 9
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		var operation *net.OpError
		if errors.As(err, &operation) {
			switch operation.Op {
			case "dial":
				return 0
			case "write":
				return 1
			case "read":
				return 2
			}
		}
		return 3
	}
	return 10
}

func TestNativeGlobalRequestFailureFactsPreserveErrorsAndBlockedReplies(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}, "dial_timeout"},
		{&net.OpError{Op: "write", Err: os.ErrDeadlineExceeded}, "write_timeout"},
		{&net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}, "read_timeout"},
		{context.DeadlineExceeded, "timeout"},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "refused"},
		{&net.OpError{Op: "read", Err: syscall.ECONNRESET}, "reset"},
		{syscall.EADDRNOTAVAIL, "address_unavailable"},
		{syscall.EMFILE, "resource_limit"},
		{syscall.ENFILE, "resource_limit"},
		{syscall.ENOBUFS, "resource_limit"},
		{syscall.ENOMEM, "resource_limit"},
		{io.EOF, "eof"},
		{io.ErrUnexpectedEOF, "eof"},
		{io.ErrShortWrite, "short_write"},
		{context.Canceled, "other"},
		{errors.New("private target and credential"), "other"},
	} {
		wrapped := fmt.Errorf("private endpoint and path: %w", tc.err)
		index := nativeRequestFailureIndex(wrapped)
		if index < 0 || index >= len(nativeRequestFailureKinds) || nativeRequestFailureKinds[index] != tc.want || !errors.Is(wrapped, tc.err) {
			t.Fatal("failure category lost the actual error class or modified its cause")
		}
	}
	if nativeRequestFailureIndex(nil) != -1 {
		t.Fatal("successful request must not fabricate a failure category")
	}
	for _, tc := range []struct {
		reply []byte
		ok    bool
	}{{nil, true}, {[]byte{0, 0}, true}, {[]byte{0}, false}, {[]byte{1, 0}, false}, {[]byte{0, 0, 0, 0}, false}} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		nativeMust(t, err, "create owned blocked-response transport")
		t.Cleanup(func() { _ = listener.Close() })
		nativeMust(t, listener.(*net.TCPListener).SetDeadline(time.Now().Add(10*time.Second)), "bound owned transport acceptance")
		done := make(chan error, 1)
		go func() {
			connection, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			defer connection.Close()
			var header [3]byte
			if _, err := io.ReadFull(connection, header[:]); err != nil {
				done <- err
				return
			}
			if header != [3]byte{1, 2, 3} {
				done <- errors.New("owned header changed")
				return
			}
			_, err = connection.Write(tc.reply)
			done <- err
		}()
		sent := 0
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		failure := nativeLoadBlockedRequestFailure(ctx, listener.Addr().(*net.TCPAddr).Port, []byte{1, 2, 3}, func() { sent++ })
		cancel()
		nativeMust(t, <-done, "complete owned blocked-response transport")
		_ = listener.Close()
		if (failure == nil) != tc.ok || sent != 1 {
			t.Fatal("failure observation changed blocked response acceptance or delivery accounting")
		}
	}
}
