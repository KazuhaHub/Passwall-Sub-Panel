package xui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// This probe is the only thing standing between an operator and a limit that
// is stored, reported back, and enforced by nothing. Each case names the
// wrong answer it prevents.

func fail2banServer(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panel/api/server/fail2banStatus" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
}

func TestGetFail2banStatus_ReadsEveryGate(t *testing.T) {
	c := fail2banServer(t, http.StatusOK,
		`{"success":true,"obj":{"enabled":true,"installed":true,"usable":true,"windows":false}}`)
	got, err := c.GetFail2banStatus(context.Background())
	if err != nil {
		t.Fatalf("GetFail2banStatus: %v", err)
	}
	want := domain.Fail2banStatus{Enabled: true, Installed: true, Usable: true}
	if *got != want {
		t.Fatalf("status = %+v, want %+v", *got, want)
	}
}

// The gates are independent and the classification reads three of them, so a
// field silently dropped in decoding would turn a dead limit into a working
// one. Asserted field by field rather than through the happy path above,
// which cannot distinguish "decoded" from "happened to be true".
func TestGetFail2banStatus_DoesNotLoseAGate(t *testing.T) {
	c := fail2banServer(t, http.StatusOK,
		`{"success":true,"obj":{"enabled":true,"installed":false,"usable":false,"windows":true}}`)
	got, err := c.GetFail2banStatus(context.Background())
	if err != nil {
		t.Fatalf("GetFail2banStatus: %v", err)
	}
	switch {
	case !got.Enabled:
		t.Fatal("enabled was dropped")
	case got.Installed:
		t.Fatal("installed was not read as false")
	case got.Usable:
		t.Fatal("usable was not read as false")
	case !got.Windows:
		t.Fatal("windows was dropped — a windows node would be reported as broken")
	}
	if state := domain.ClassifyIPLimit(*got); state != domain.IPLimitEnforcementDisconnectOnly {
		t.Fatalf("state = %q, want disconnect_only", state)
	}
}

// The trap this probe was built for: XUI_ENABLE_FAIL2BAN=1 looks like "on" and
// turns enforcement off. Upstream reports it as enabled:false, and an adapter
// that never read that field would report those nodes as fine forever — the
// one operator mistake most likely to be sitting in a real fleet.
func TestGetFail2banStatus_ReadsTheEnvVarGate(t *testing.T) {
	c := fail2banServer(t, http.StatusOK,
		`{"success":true,"obj":{"enabled":false,"installed":true,"usable":false,"windows":false}}`)
	got, err := c.GetFail2banStatus(context.Background())
	if err != nil {
		t.Fatalf("GetFail2banStatus: %v", err)
	}
	if got.Enabled {
		t.Fatal("enabled was not read; a node with XUI_ENABLE_FAIL2BAN=1 would report as working")
	}
	if state := domain.ClassifyIPLimit(*got); state != domain.IPLimitEnforcementDisabled {
		t.Fatalf("state = %q, want disabled", state)
	}
}

// The adapter REPORTS the gates; it does not derive them. Upstream's `usable`
// is enabled && installed today, so recomputing it here would agree on every
// real 3.7.0 response — and would silently stop agreeing the moment upstream
// adds a third gate, leaving PSP claiming enforcement that had quietly ended.
// Asserted with a response no current panel sends, because that is exactly the
// shape a future one would.
func TestGetFail2banStatus_ReportsUsableRatherThanDerivingIt(t *testing.T) {
	c := fail2banServer(t, http.StatusOK,
		`{"success":true,"obj":{"enabled":true,"installed":true,"usable":false,"windows":false}}`)
	got, err := c.GetFail2banStatus(context.Background())
	if err != nil {
		t.Fatalf("GetFail2banStatus: %v", err)
	}
	if got.Usable {
		t.Fatal("usable was recomputed from the other gates instead of read; " +
			"a future upstream gate would then be invisible to PSP")
	}
	if state := domain.ClassifyIPLimit(*got); state != domain.IPLimitEnforcementNotInstalled {
		t.Fatalf("state = %q, want the node to stop counting as enforced", state)
	}
}

// A panel older than 3.7.0 has no such route. It must arrive as
// ErrXUIEndpointUnsupported so the caller can record "this version cannot
// tell us" instead of accusing a node that may well be enforcing fine.
func TestGetFail2banStatus_OldPanelIsUnsupportedNotBroken(t *testing.T) {
	c := fail2banServer(t, http.StatusNotFound, `404 page not found`)
	_, err := c.GetFail2banStatus(context.Background())
	if !errors.Is(err, ports.ErrXUIEndpointUnsupported) {
		t.Fatalf("err = %v, want ErrXUIEndpointUnsupported", err)
	}
}

// The optional-interface contract: nothing declares Fail2banReader, so a
// broken signature would compile and silently disable the probe for the whole
// fleet — the type assertion in the probe would just stop matching.
func TestClientSatisfiesFail2banReader(t *testing.T) {
	var _ ports.Fail2banReader = (*Client)(nil)
}
