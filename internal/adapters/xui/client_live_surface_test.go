package xui

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// liveSurfaceClient builds the adapter the same way the other TestLive_* cases
// do: in-package, with a permissive http.Client instead of New(). New() installs
// safehttp.BlockNonPublicDial + real TLS verification, both of which correctly
// refuse the loopback / private address a scratch panel runs on. Bearer-token
// mode needs nothing but baseURL + apiToken.
func liveSurfaceClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	if base == "" || token == "" {
		t.Skip("set PSP_LIVE_XUI_URL and PSP_LIVE_XUI_TOKEN to run the live 3X-UI smoke test")
	}
	return &Client{
		baseURL:  strings.TrimRight(base, "/"),
		apiToken: token,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // local smoke test only
		},
	}, context.Background()
}

// offlineGitHubEnv opts the two GitHub-backed server routes out of a hard
// failure. getPanelUpdateInfo and getXrayVersion answer from a lookup the PANEL
// performs against api.github.com, so on an air-gapped or egress-filtered
// validation host they return 3X-UI's own failure envelope however healthy the
// endpoint is. Tolerating that unconditionally would also swallow a real
// response-shape regression, so it takes a deliberate declaration; route
// presence is asserted either way.
const offlineGitHubEnv = "PSP_LIVE_XUI_NO_GITHUB"

func githubBackedRouteIsOffline() bool {
	return os.Getenv(offlineGitHubEnv) != ""
}

// TestLive_XUISurface is the 3X-UI counterpart of TestLive_SUISurface: it drives
// EVERY endpoint this adapter calls against a REAL 3X-UI panel, so
// docs/compat/v3.json's max_tested_xui can be raised from observed behaviour
// rather than from release notes. The pre-existing TestLive_* cases each probe
// one narrow shared-client invariant and require the panel to already have >= 2
// inbounds; this one is self-contained — it creates its own two scratch inbounds
// and tears everything down — so it can be pointed at a freshly-installed panel.
//
//	PSP_LIVE_XUI_URL='http://127.0.0.1:54321/basepath' \
//	PSP_LIVE_XUI_TOKEN='<api-token>' \
//	  go test ./internal/adapters/xui/ -run TestLive_XUISurface -v
//
// Scratch inbounds are created DISABLED and on high ports, so nothing ever binds
// and a stray port collision can't take down a real service. Point this at a
// scratch panel; do NOT run it against production.
//
// Deliberately NOT exercised: UpdatePanel and InstallXray. Both are destructive
// (the panel replaces its own binary and restarts), so this test asserts only
// that the adapter can reach them — route presence is checked out-of-band during
// compat validation, exactly as the 3.4.2 / 3.5.0 verifications did.
func TestLive_XUISurface(t *testing.T) {
	c, ctx := liveSurfaceClient(t)

	// --- Server read path -------------------------------------------------
	// GetServerStatus.PanelVersion is the single field the compat gate reads
	// (version.CheckXUI), so an empty value silently disables gating entirely.
	st, err := c.GetServerStatus(ctx)
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}
	if st.PanelVersion == "" {
		t.Fatal("GetServerStatus returned an empty PanelVersion — the compat gate reads this field")
	}
	t.Logf("panelVersion=%q xrayVersion=%q xrayState=%q", st.PanelVersion, st.XrayVersion, st.XrayState)

	// getPanelUpdateInfo's answer comes from a GitHub release lookup the PANEL
	// makes, so a panel with no route to api.github.com returns 3X-UI's own
	// {success:false} envelope no matter how healthy the endpoint is. That is an
	// environment fact, not a compat signal — but it is indistinguishable here
	// from a real response-shape regression, so it is tolerated ONLY when the
	// operator says the panel is offline (see githubBackedRouteIsOffline). The
	// route must exist either way: a 404 is always fatal, which is precisely the
	// regression this smoke exists to catch.
	switch info, err := c.GetPanelUpdateInfo(ctx); {
	case errors.Is(err, ports.ErrXUIEndpointUnsupported):
		t.Fatalf("GetPanelUpdateInfo: route missing on this panel: %v", err)
	case err != nil && githubBackedRouteIsOffline():
		t.Logf("GetPanelUpdateInfo: route answered; the panel's own GitHub lookup failed as declared by %s: %v", offlineGitHubEnv, err)
	case err != nil:
		t.Fatalf("GetPanelUpdateInfo: %v (set %s=1 if this panel genuinely cannot reach api.github.com)", err, offlineGitHubEnv)
	default:
		t.Logf("updateInfo current=%q latest=%q available=%v", info.CurrentVersion, info.LatestVersion, info.UpdateAvailable)
	}

	// getXrayVersion returns the list of installable xray tags (fetched by the
	// panel from GitHub), not the running version — an empty list is a valid
	// answer on a panel with no upstream network. Same GitHub-backed caveat and
	// same opt-in as getPanelUpdateInfo above.
	switch vers, err := c.GetXrayVersionList(ctx); {
	case errors.Is(err, ports.ErrXUIEndpointUnsupported):
		t.Fatalf("GetXrayVersionList: route missing on this panel: %v", err)
	case err != nil && githubBackedRouteIsOffline():
		t.Logf("GetXrayVersionList: route answered; the panel's own GitHub fetch failed as declared by %s: %v", offlineGitHubEnv, err)
	case err != nil:
		t.Fatalf("GetXrayVersionList: %v (set %s=1 if this panel genuinely cannot reach api.github.com)", err, offlineGitHubEnv)
	default:
		t.Logf("xray version list: %d entries", len(vers))
	}

	// getWebCertFiles first shipped in 3.2.7; on older panels doJSON maps the
	// 404 to ports.ErrXUIEndpointUnsupported. Anything at/above the current
	// floor must answer it, so a plain error here is a real regression.
	certs, err := c.GetWebCertFiles(ctx)
	if err != nil {
		t.Fatalf("GetWebCertFiles: %v", err)
	}
	t.Logf("webCertFile=%q webKeyFile=%q", certs.CertFile, certs.KeyFile)

	// --- Inbound read path ------------------------------------------------
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

	// --- Inbound lifecycle ------------------------------------------------
	// Two inbounds, because the whole point of the v3.9.0 shared-client model
	// is one client spanning MANY inbounds; a single-inbound smoke would not
	// exercise attach/detach or the client_inbounds junction at all.
	settings, _ := json.Marshal(map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}})
	stream, _ := json.Marshal(map[string]any{"network": "tcp", "security": "none"})
	sniffing, _ := json.Marshal(map[string]any{"enabled": false, "destOverride": []string{"http", "tls"}})

	mkInbound := func(remark string, port int) int {
		t.Helper()
		id, err := c.AddInbound(ctx, ports.InboundSpec{
			Remark: remark, Enable: false, Listen: "", Port: port,
			Protocol: "vless", Settings: string(settings),
			StreamSettings: string(stream), Sniffing: string(sniffing),
		})
		if err != nil {
			t.Fatalf("AddInbound(%s): %v", remark, err)
		}
		t.Cleanup(func() { _ = c.DelInbound(context.Background(), id) })
		return id
	}
	stamp := time.Now().UnixNano()
	inbA := mkInbound(fmt.Sprintf("psp-surface-a-%d", stamp), 39001)
	inbB := mkInbound(fmt.Sprintf("psp-surface-b-%d", stamp), 39002)
	t.Logf("scratch inbounds: a=%d b=%d", inbA, inbB)

	if after, err := c.ListInbounds(ctx); err != nil {
		t.Fatalf("ListInbounds after add: %v", err)
	} else if len(after) != len(before)+2 {
		t.Fatalf("inbound count = %d, want %d", len(after), len(before)+2)
	}

	gotA, err := c.GetInbound(ctx, inbA)
	if err != nil {
		t.Fatalf("GetInbound(%d): %v", inbA, err)
	}
	if gotA.Port != 39001 {
		t.Fatalf("GetInbound port = %d, want 39001", gotA.Port)
	}
	if gotA.Enable {
		t.Fatal("scratch inbound came back enabled — it must stay disabled so it never binds")
	}

	// UpdateInbound is a read-modify-write in production (inboundcfg merges live
	// clients back in); here it only has to prove the round-trip persists.
	newRemark := gotA.Remark + "-edited"
	if err := c.UpdateInbound(ctx, inbA, ports.InboundSpec{
		Remark: newRemark, Enable: false, Listen: "", Port: 39003,
		Protocol: "vless", Settings: string(settings),
		StreamSettings: string(stream), Sniffing: string(sniffing),
	}); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got, err := c.GetInbound(ctx, inbA); err != nil {
		t.Fatalf("GetInbound after update: %v", err)
	} else if got.Remark != newRemark || got.Port != 39003 {
		t.Fatalf("update not reflected: remark=%q port=%d", got.Remark, got.Port)
	}

	// setEnable is its own route (not folded into update), so it needs its own
	// round-trip. Flip on then straight back off — leaving a scratch inbound
	// enabled would make it bind on the next xray reload.
	if err := c.SetInboundEnable(ctx, inbA, true); err != nil {
		t.Fatalf("SetInboundEnable(true): %v", err)
	}
	if got, err := c.GetInbound(ctx, inbA); err != nil {
		t.Fatalf("GetInbound after setEnable: %v", err)
	} else if !got.Enable {
		t.Fatal("SetInboundEnable(true) did not persist")
	}
	if err := c.SetInboundEnable(ctx, inbA, false); err != nil {
		t.Fatalf("SetInboundEnable(false): %v", err)
	}

	// --- Shared-client lifecycle -----------------------------------------
	email := fmt.Sprintf("psp-surface-%d@psp.local", stamp)
	t.Cleanup(func() { _ = c.DelClientByEmail(context.Background(), inbA, email) })

	// One client, both inbounds, single call — the shared-client create path.
	if err := c.AddClientToInbounds(ctx, []int{inbA, inbB}, ports.ClientSpec{
		Email: email, Enable: true, ID: "5f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e70",
	}); err != nil {
		t.Fatalf("AddClientToInbounds: %v", err)
	}
	assertAttached(t, c, ctx, email, inbA, inbB)

	detail, err := c.GetClient(ctx, email)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	// ClientDetail.ID is mapped from client.uuid, NOT the numeric DB row id —
	// a silent swap of those two would break every credential PSP renders.
	if detail.ID != "5f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e70" {
		t.Fatalf("GetClient ID = %q, want the uuid we created (is it mapping client.id instead of client.uuid?)", detail.ID)
	}
	if !detail.Enable {
		t.Fatal("GetClient Enable = false, want true")
	}

	// update-by-email is a FULL REPLACE on 3X-UI: whatever the spec omits is
	// cleared. PSP relies on that (it always sends a complete spec), so the
	// assertion is that a changed field lands AND the uuid survives.
	if err := c.UpdateClient(ctx, ports.ClientSpec{
		Email: email, Enable: false, ID: detail.ID, TotalGB: 2 << 30,
	}); err != nil {
		t.Fatalf("UpdateClient: %v", err)
	}
	if got, err := c.GetClient(ctx, email); err != nil {
		t.Fatalf("GetClient after update: %v", err)
	} else {
		if got.Enable {
			t.Fatal("update did not persist enable=false")
		}
		if got.TotalGB != 2<<30 {
			t.Fatalf("update did not persist totalGB: got %d, want %d", got.TotalGB, int64(2<<30))
		}
		if got.ID != detail.ID {
			t.Fatalf("update changed the uuid: %q -> %q", detail.ID, got.ID)
		}
	}

	// detach / attach must be idempotent-friendly: reconcile computes an
	// attachment delta and re-applies it, so a re-attach of an already-attached
	// inbound must not explode with "email already in use".
	if err := c.DetachClient(ctx, email, []int{inbB}); err != nil {
		t.Fatalf("DetachClient: %v", err)
	}
	assertAttached(t, c, ctx, email, inbA)
	if err := c.AttachClient(ctx, email, []int{inbB}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	assertAttached(t, c, ctx, email, inbA, inbB)

	// Bulk attach/detach are the node-warm-up path (one xray reload instead of N).
	if _, err := c.BulkDetach(ctx, []string{email}, []int{inbB}); err != nil {
		t.Fatalf("BulkDetach: %v", err)
	}
	assertAttached(t, c, ctx, email, inbA)
	if _, err := c.BulkAttach(ctx, []string{email}, []int{inbB}); err != nil {
		t.Fatalf("BulkAttach: %v", err)
	}
	assertAttached(t, c, ctx, email, inbA, inbB)

	if err := c.DelClientByEmail(ctx, inbA, email); err != nil {
		t.Fatalf("DelClientByEmail: %v", err)
	}
	// Unlike the S-UI adapter (which reports a miss as (nil,nil) from a clean
	// response), 3X-UI answers HTTP 200 + {success:false,msg:" (record not
	// found)"}. isClientNotFoundMsg has to recognise that wording — a reworded
	// upstream message would turn "absent" into a transport error and make
	// reconcile re-create clients forever.
	if got, err := c.GetClient(ctx, email); err != nil {
		t.Fatalf("GetClient after delete returned an error (isClientNotFoundMsg missed the wording?): %v", err)
	} else if got != nil {
		t.Fatalf("GetClient after delete returned %+v, want nil", got)
	}

	// --- Bulk create / delete --------------------------------------------
	bulkEmail := fmt.Sprintf("psp-surface-bulk-%d@psp.local", stamp)
	t.Cleanup(func() { _, _ = c.BulkDelByEmail(context.Background(), []string{bulkEmail}) })
	res, err := c.BulkCreateClients(ctx, []ports.BulkCreateClientItem{{
		Spec:       ports.ClientSpec{Email: bulkEmail, Enable: true, ID: "5f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e71"},
		InboundIDs: []int{inbA, inbB},
	}})
	if err != nil {
		t.Fatalf("BulkCreateClients: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("BulkCreateClients created = %d, want 1", res.Created)
	}
	assertAttached(t, c, ctx, bulkEmail, inbA, inbB)

	deleted, err := c.BulkDelByEmail(ctx, []string{bulkEmail})
	if err != nil {
		t.Fatalf("BulkDelByEmail: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("BulkDelByEmail deleted = %d, want 1", deleted)
	}

	// --- Single-inbound (per-node era) client path ------------------------
	// AddClient / UpdateClient are the pre-v3.9.0 single-inbound calls. They are
	// still live code (the v3.6.2-v3.8.x compat entry covers panels driven that
	// way), so a bump of max_tested_xui has to cover them too.
	legacyEmail := fmt.Sprintf("psp-surface-legacy-%d@psp.local", stamp)
	t.Cleanup(func() { _ = c.DelClientByEmail(context.Background(), inbA, legacyEmail) })
	if err := c.AddClient(ctx, inbA, ports.ClientSpec{
		Email: legacyEmail, Enable: true, ID: "5f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e72",
	}); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	assertAttached(t, c, ctx, legacyEmail, inbA)
	if err := c.UpdateClient(ctx, ports.ClientSpec{
		Email: legacyEmail, Enable: false, ID: "5f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e72",
	}); err != nil {
		t.Fatalf("UpdateClient: %v", err)
	}
	if got, err := c.GetClient(ctx, legacyEmail); err != nil {
		t.Fatalf("GetClient(legacy) after update: %v", err)
	} else if got.Enable {
		t.Fatal("UpdateClient did not persist enable=false")
	}
	if err := c.DelClientByEmail(ctx, inbA, legacyEmail); err != nil {
		t.Fatalf("DelClientByEmail(legacy): %v", err)
	}

	// --- Inbound teardown -------------------------------------------------
	// Explicit deletes (Cleanup also covers them, but a failure here is a real
	// signal that /inbounds/del changed).
	if err := c.DelInbound(ctx, inbB); err != nil {
		t.Fatalf("DelInbound(b): %v", err)
	}
	if err := c.DelInbound(ctx, inbA); err != nil {
		t.Fatalf("DelInbound(a): %v", err)
	}
	if after, err := c.ListInbounds(ctx); err != nil {
		t.Fatalf("ListInbounds after teardown: %v", err)
	} else if len(after) != len(before) {
		t.Fatalf("inbound count after teardown = %d, want %d (scratch inbounds leaked)", len(after), len(before))
	}
}

// TestLive_XUIRealityScan covers /panel/api/server/scanRealityTargets on its own
// because it is the route that set the current compiled floor: MinXUI rose to
// 3.4.2 precisely because this endpoint does not exist on 3.4.1 or earlier, and
// PSP ships no local fallback. It reaches the public internet (the PANEL probes
// the target, not the test host), so it is separated out and tolerates a
// scan that returns no usable rows — the contract under test is that the route
// exists and the adapter can parse its reply, not that any given host qualifies.
func TestLive_XUIRealityScan(t *testing.T) {
	c, ctx := liveSurfaceClient(t)

	results, err := c.ScanRealityTargets(ctx, "www.microsoft.com")
	if err != nil {
		t.Fatalf("ScanRealityTargets: %v (route missing => panel is below the 3.4.2 floor)", err)
	}
	t.Logf("reality scan returned %d row(s)", len(results))
	for _, r := range results {
		t.Logf("  %+v", r)
	}
}

// TestLive_XUIBulkSetEnabled verifies the bulk enable/disable path against a
// real panel. It is the collapse of the month-rollover fan-out: traffic periods
// are calendar-aligned, so every quota-suspended user resumes on the same poll,
// and this turns N /clients/update writes (N xray reloads on one panel) into one.
//
// Asserts the flag actually MOVED on the panel, not merely that the call
// returned success — the endpoint reports per-email refusals in `skipped` while
// still answering 200, so a green response proves nothing on its own.
func TestLive_XUIBulkSetEnabled(t *testing.T) {
	c, ctx := liveSurfaceClient(t)

	inbounds, err := c.ListInbounds(ctx)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) < 1 {
		t.Skip("need at least one inbound")
	}
	inb := inbounds[0].ID

	stamp := time.Now().UnixNano()
	emails := []string{
		fmt.Sprintf("psp-bulk-a-%d@psp.local", stamp),
		fmt.Sprintf("psp-bulk-b-%d@psp.local", stamp),
	}
	for i, e := range emails {
		if err := c.AddClientToInbounds(ctx, []int{inb}, ports.ClientSpec{
			Email: e, Enable: true, ID: fmt.Sprintf("7f1e6a1c-6b6a-4f0e-9f4a-1f2b3c4d5e8%d", i),
		}); err != nil {
			t.Fatalf("seed %s: %v", e, err)
		}
		t.Cleanup(func() { _ = c.DelClientByEmail(context.Background(), inb, e) })
	}

	assertEnabled := func(want bool) {
		t.Helper()
		for _, e := range emails {
			got, err := c.GetClient(ctx, e)
			if err != nil {
				t.Fatalf("GetClient(%s): %v", e, err)
			}
			if got.Enable != want {
				t.Fatalf("client %s Enable = %v, want %v", e, got.Enable, want)
			}
		}
	}

	res, err := c.BulkSetEnabled(ctx, emails, false)
	if err != nil {
		t.Fatalf("BulkSetEnabled(false): %v", err)
	}
	if res.Changed != len(emails) {
		t.Fatalf("disable changed = %d, want %d (skipped: %+v)", res.Changed, len(emails), res.Skipped)
	}
	assertEnabled(false)

	res, err = c.BulkSetEnabled(ctx, emails, true)
	if err != nil {
		t.Fatalf("BulkSetEnabled(true): %v", err)
	}
	if res.Changed != len(emails) {
		t.Fatalf("enable changed = %d, want %d (skipped: %+v)", res.Changed, len(emails), res.Skipped)
	}
	assertEnabled(true)

	// A nonexistent email must be reported, not silently counted as flipped.
	res, err = c.BulkSetEnabled(ctx, []string{emails[0], "psp-bulk-ghost@psp.local"}, false)
	if err != nil {
		t.Fatalf("BulkSetEnabled with a ghost: %v", err)
	}
	t.Logf("ghost run: changed=%d skipped=%+v", res.Changed, res.Skipped)
	if res.Changed != 1 {
		t.Fatalf("changed = %d, want 1 (only the real client)", res.Changed)
	}
	_, _ = c.BulkSetEnabled(ctx, emails, true)
}
