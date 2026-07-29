package sui

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestLive_SUISurface exercises the S-UI endpoints this adapter actually calls
// against a REAL S-UI panel, so docs/compat/v3.json's sui_entries can be filled
// in from observed behaviour instead of release notes. Gated on env vars and
// skipped by default (no secrets in the repo), mirroring the 3X-UI live smoke in
// ../xui/client_live_test.go. Run with:
//
//	PSP_LIVE_SUI_URL='http://host:2095/app' \
//	PSP_LIVE_SUI_TOKEN='<api-token>' \
//	  go test ./internal/adapters/sui/ -run TestLive_SUISurface -v
//
// The token comes from the panel's own /api/addToken (Settings → API tokens).
// Everything it creates is torn down in t.Cleanup, but S-UI inbounds cannot be
// created disabled (see the inbound section below), so the scratch inbound does
// briefly listen. Point this at a scratch panel; do NOT run it against production.
func liveClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	base := os.Getenv("PSP_LIVE_SUI_URL")
	token := os.Getenv("PSP_LIVE_SUI_TOKEN")
	if base == "" || token == "" {
		t.Skip("set PSP_LIVE_SUI_URL and PSP_LIVE_SUI_TOKEN to run the live S-UI smoke test")
	}
	// Construct in-package with a permissive http client rather than via New():
	// New() installs safehttp.BlockNonPublicDial, which correctly refuses the
	// loopback/private address a scratch panel runs on. Token mode needs only
	// baseURL + token.
	return &Client{
		panelName: "live",
		baseURL:   strings.TrimRight(base, "/"),
		token:     token,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // local smoke test only
		},
	}, context.Background()
}

// TestLive_SUISurface covers the read path plus the full inbound and client
// lifecycles, which together are every capability the adapter advertises.
func TestLive_SUISurface(t *testing.T) {
	c, ctx := liveClient(t)

	// --- Read path -------------------------------------------------------
	st, err := c.GetServerStatus(ctx)
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}
	if st.PanelVersion == "" {
		t.Fatal("GetServerStatus returned an empty PanelVersion — the compat gate reads this field")
	}
	t.Logf("panel version = %q, xray/core state = %q", st.PanelVersion, st.XrayState)

	before, err := c.ListInbounds(ctx)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if _, err := c.ListInboundsSlim(ctx); err != nil {
		t.Fatalf("ListInboundsSlim: %v", err)
	}
	if _, err := c.ListClientInbounds(ctx); err != nil {
		t.Fatalf("ListClientInbounds: %v", err)
	}

	// --- Inbound lifecycle ----------------------------------------------
	// NOTE: unlike the 3X-UI smoke, which parks its scratch inbounds in the
	// disabled state, S-UI has no per-inbound enable flag at all — the adapter
	// rejects Enable=false up front ("S-UI inbounds are always enabled and
	// cannot persist enable=false"), and SetInboundEnable is an unsupported
	// write. The scratch inbound therefore really does bind, so it listens on
	// 127.0.0.1 on a high port and is deleted in Cleanup.
	const remark = "psp-livetest-inbound"
	settings, _ := json.Marshal(map[string]any{"clients": []any{}})
	stream, _ := json.Marshal(map[string]any{"security": "none", "network": "tcp"})
	id, err := c.AddInbound(ctx, ports.InboundSpec{
		Remark: remark, Enable: true, Listen: "127.0.0.1", Port: 45871,
		Protocol: "vless", Settings: string(settings), StreamSettings: string(stream),
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	t.Cleanup(func() { _ = c.DelInbound(context.Background(), id) })
	t.Logf("created inbound id=%d", id)

	got, err := c.GetInbound(ctx, id)
	if err != nil {
		t.Fatalf("GetInbound(%d): %v", id, err)
	}
	if got.Remark != remark {
		t.Fatalf("GetInbound remark = %q, want %q", got.Remark, remark)
	}
	if got.Port != 45871 {
		t.Fatalf("GetInbound port = %d, want 45871", got.Port)
	}

	if err := c.UpdateInbound(ctx, id, ports.InboundSpec{
		Remark: remark + "-edited", Enable: true, Listen: "127.0.0.1", Port: 45872,
		Protocol: "vless", Settings: string(settings), StreamSettings: string(stream),
	}); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	got, err = c.GetInbound(ctx, id)
	if err != nil {
		t.Fatalf("GetInbound after update: %v", err)
	}
	if got.Remark != remark+"-edited" || got.Port != 45872 {
		t.Fatalf("update not reflected: remark=%q port=%d", got.Remark, got.Port)
	}

	if after, err := c.ListInbounds(ctx); err != nil {
		t.Fatalf("ListInbounds after add: %v", err)
	} else if len(after) != len(before)+1 {
		t.Fatalf("inbound count = %d, want %d", len(after), len(before)+1)
	}

	// --- Client lifecycle ------------------------------------------------
	email := fmt.Sprintf("psp-livetest-%d@psp.local", time.Now().UnixNano())
	t.Cleanup(func() { _ = c.DelClientByEmail(context.Background(), id, email) })

	if err := c.AddClientToInbounds(ctx, []int{id}, ports.ClientSpec{
		Email: email, Enable: true, ID: "6f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e6f",
	}); err != nil {
		t.Fatalf("AddClientToInbounds: %v", err)
	}
	detail, err := c.GetClient(ctx, email)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if !containsInt(detail.InboundIDs, id) {
		t.Fatalf("GetClient InboundIDs = %v, want to contain %d", detail.InboundIDs, id)
	}

	if err := c.UpdateClientWithInbound(ctx, got, detail.ID, ports.ClientSpec{
		Email: email, Enable: false, ID: detail.ID,
	}); err != nil {
		t.Fatalf("UpdateClientWithInbound: %v", err)
	}

	if err := c.DetachClient(ctx, email, []int{id}); err != nil {
		t.Fatalf("DetachClient: %v", err)
	}
	if err := c.AttachClient(ctx, email, []int{id}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	if err := c.DelClientByEmail(ctx, id, email); err != nil {
		t.Fatalf("DelClientByEmail: %v", err)
	}
	// Not-found semantics differ from the 3X-UI adapter, which surfaces a
	// "record not found" error: this adapter reports a miss as (nil, nil), so
	// assert on the detail rather than on err.
	if detail, err := c.GetClient(ctx, email); err != nil {
		t.Fatalf("GetClient after delete returned an error: %v", err)
	} else if detail != nil {
		t.Fatalf("GetClient after delete returned %+v, want nil (client should be gone)", detail)
	}

	if err := c.DelInbound(ctx, id); err != nil {
		t.Fatalf("DelInbound: %v", err)
	}
}

func containsInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
