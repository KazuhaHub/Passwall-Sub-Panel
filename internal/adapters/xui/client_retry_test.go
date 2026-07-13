package xui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestIsInboundConflict(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"", false},
		{"some other error", false},
		{"email already in use: u1@psp.local", false},
		{"POST /panel/api/clients/update/u1: Something went wrong (UNIQUE constraint failed: client_inbounds.client_id, client_inbounds.inbound_id)", true},
		{"UNIQUE constraint failed: CLIENT_INBOUNDS", true}, // case-insensitive
	}
	for _, tc := range cases {
		var err error
		if tc.msg != "" {
			err = &stringErr{tc.msg}
		}
		if got := isInboundConflict(err); got != tc.want {
			t.Errorf("isInboundConflict(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }

func TestIsPermanentPanelMsg(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"", false},
		{"connection reset by peer", false},
		{"Duplicate email: u1@psp.local", true},
		{"email already exists", true},
		{"port already exists", true}, // legacy AddInbound wording (via "already exist")
		// AddInbound port conflict, 3X-UI >= 3.4.2 wording — LIVE-observed on 3.5.0.
		{"Something went wrong (port 443 (tcp) already used by inbound 'USA NY - Host' (#1) on *\n)", true},
		{"PORT 443 (TCP) ALREADY USED BY INBOUND", true}, // case-insensitive
		{"client not found", true},
		{"not found in inbound", true},
	}
	for _, tc := range cases {
		if got := isPermanentPanelMsg(tc.msg); got != tc.want {
			t.Errorf("isPermanentPanelMsg(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// conflictServer returns a 3X-UI-shaped server that replies with the transient
// client_inbounds conflict for the first failFor calls, then success.
func conflictServer(failFor int, calls *int32) *httptest.Server {
	const conflict = `{"success":false,"msg":"Something went wrong (UNIQUE constraint failed: client_inbounds.client_id, client_inbounds.inbound_id)"}`
	const ok = `{"success":true,"msg":"ok"}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if int(n) <= failFor {
			_, _ = w.Write([]byte(conflict))
		} else {
			_, _ = w.Write([]byte(ok))
		}
	}))
}

// TestMutateWithRetry_RecoversFromCrossProcessConflict pins the v3.9.0-beta.7 fix:
// a transient client_inbounds conflict (a cross-process / multi-instance race the
// per-email lock can't cover) must be retried until it clears, so the migrate task
// succeeds instead of looping forever.
func TestMutateWithRetry_RecoversFromCrossProcessConflict(t *testing.T) {
	var calls int32
	srv := conflictServer(2, &calls) // conflict twice, then succeed
	defer srv.Close()
	c := &Client{baseURL: srv.URL, apiToken: "t", http: srv.Client()}

	if err := c.UpdateClient(context.Background(), 0, "uuid", ports.ClientSpec{Email: "u1@psp.local", Enable: true}); err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts (2 conflicts + 1 success), got %d", got)
	}
}

func TestMutateWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	var calls int32
	srv := conflictServer(99, &calls) // always conflict
	defer srv.Close()
	c := &Client{baseURL: srv.URL, apiToken: "t", http: srv.Client()}

	err := c.UpdateClient(context.Background(), 0, "uuid", ports.ClientSpec{Email: "u1@psp.local"})
	if err == nil || !strings.Contains(err.Error(), "client_inbounds") {
		t.Fatalf("expected the client_inbounds error to surface after exhausting retries, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Fatalf("expected 5 bounded attempts, got %d", got)
	}
}

func TestMutateWithRetry_DoesNotRetryOtherErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"success":false,"msg":"email already in use: u1@psp.local"}`))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, apiToken: "t", http: srv.Client()}

	if err := c.UpdateClient(context.Background(), 0, "uuid", ports.ClientSpec{Email: "u1@psp.local"}); err == nil {
		t.Fatal("expected an error")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("a non-conflict error must NOT be retried: got %d calls, want 1", got)
	}
}
