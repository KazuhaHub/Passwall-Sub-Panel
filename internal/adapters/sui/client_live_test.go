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

// TestLive_SUIBulkSetEnabled exercises the S-UI half of the BulkSetEnabled
// contract against a REAL panel.
//
// It gets its own live test because the S-UI implementation is structurally
// different from 3X-UI's: S-UI has no bulkEnable route, so this reads each full
// client row and issues ONE clients/editbulk save. editbulk performs a FULL-ROW
// write, so the per-client GET is load-bearing — submitting the summary that
// GET /clients returns (which omits config and links) would erase credentials.
// That failure mode is invisible to a unit test with a fake, which is exactly
// why this runs against a panel and asserts the credentials survived.
func TestLive_SUIBulkSetEnabled(t *testing.T) {
	c, ctx := liveClient(t)

	inbounds, err := c.ListInbounds(ctx)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	var inbID int
	if len(inbounds) > 0 {
		inbID = inbounds[0].ID
	} else {
		settings, _ := json.Marshal(map[string]any{"clients": []any{}})
		stream, _ := json.Marshal(map[string]any{"security": "none", "network": "tcp"})
		inbID, err = c.AddInbound(ctx, ports.InboundSpec{
			Remark: "psp-bulk-live", Enable: true, Listen: "127.0.0.1", Port: 45899,
			Protocol: "vless", Settings: string(settings), StreamSettings: string(stream),
		})
		if err != nil {
			t.Fatalf("AddInbound: %v", err)
		}
		t.Cleanup(func() { _ = c.DelInbound(context.Background(), inbID) })
	}

	stamp := time.Now().UnixNano()
	emails := []string{
		fmt.Sprintf("psp-suibulk-a-%d@psp.local", stamp),
		fmt.Sprintf("psp-suibulk-b-%d@psp.local", stamp),
	}
	const uuidA = "8f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e90"
	for i, e := range emails {
		id := uuidA
		if i == 1 {
			id = "8f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e91"
		}
		if err := c.AddClientToInbounds(ctx, []int{inbID}, ports.ClientSpec{
			Email: e, Enable: true, ID: id,
		}); err != nil {
			t.Fatalf("seed %s: %v", e, err)
		}
		t.Cleanup(func() { _ = c.DelClientByEmail(context.Background(), inbID, e) })
	}

	assertEnabled := func(want bool) {
		t.Helper()
		for _, e := range emails {
			d, err := c.GetClient(ctx, e)
			if err != nil {
				t.Fatalf("GetClient(%s): %v", e, err)
			}
			if d == nil {
				t.Fatalf("GetClient(%s) = nil — the client vanished", e)
			}
			if d.Enable != want {
				t.Fatalf("client %s Enable = %v, want %v", e, d.Enable, want)
			}
		}
	}

	res, err := c.BulkSetEnabled(ctx, emails, false)
	if err != nil {
		t.Fatalf("BulkSetEnabled(false): %v", err)
	}
	if res.Changed != len(emails) {
		t.Fatalf("disable changed = %d, want %d (skipped %+v)", res.Changed, len(emails), res.Skipped)
	}
	assertEnabled(false)

	// THE point of running this live: editbulk is a full-row save, so a wrong
	// implementation flips enable and silently wipes the client's credentials.
	if d, err := c.GetClient(ctx, emails[0]); err != nil {
		t.Fatalf("GetClient after disable: %v", err)
	} else if d.ID != uuidA {
		t.Fatalf("client uuid = %q after the bulk write, want %q — editbulk erased credentials", d.ID, uuidA)
	}

	res, err = c.BulkSetEnabled(ctx, emails, true)
	if err != nil {
		t.Fatalf("BulkSetEnabled(true): %v", err)
	}
	if res.Changed != len(emails) {
		t.Fatalf("enable changed = %d, want %d", res.Changed, len(emails))
	}
	assertEnabled(true)

	// Already in the wanted state -> nothing to write.
	if res, err = c.BulkSetEnabled(ctx, emails, true); err != nil {
		t.Fatalf("idempotent run: %v", err)
	} else if res.Changed != 0 {
		t.Fatalf("re-enabling already-enabled clients changed = %d, want 0", res.Changed)
	}

	// An unknown email is reported, not counted as flipped.
	res, err = c.BulkSetEnabled(ctx, []string{emails[0], "psp-suibulk-ghost@psp.local"}, false)
	if err != nil {
		t.Fatalf("ghost run: %v", err)
	}
	t.Logf("ghost run: changed=%d skipped=%+v", res.Changed, res.Skipped)
	if res.Changed != 1 || len(res.Skipped) != 1 {
		t.Fatalf("changed=%d skipped=%+v, want 1 changed and 1 skipped", res.Changed, res.Skipped)
	}
	_, _ = c.BulkSetEnabled(ctx, emails, true)
}
