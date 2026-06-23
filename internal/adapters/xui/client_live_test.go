package xui

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestLive_MultiInboundClientSurface exercises the v3.9.0 attach/detach +
// multi-inbound add against a REAL 3X-UI panel. It is gated on env vars and
// skips by default (no secrets in the repo), mirroring the sqlstore live-DB
// tests' PSP_TEST_DB_* convention. Run with:
//
//	PSP_LIVE_XUI_URL='https://host:port/secretpath' \
//	PSP_LIVE_XUI_TOKEN='<api-token>' \
//	  go test ./internal/adapters/xui/ -run TestLive_MultiInboundClientSurface -v
//
// It creates one client on the first two inbounds, verifies the attachment set
// via GetClient().InboundIDs, detaches/re-attaches one, and always deletes the
// test client on the way out.
func TestLive_MultiInboundClientSurface(t *testing.T) {
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	if base == "" || token == "" {
		t.Skip("set PSP_LIVE_XUI_URL and PSP_LIVE_XUI_TOKEN to run the live 3X-UI smoke test")
	}

	// Construct the Client directly (in-package) with a PERMISSIVE http client
	// rather than via New(): New() installs safehttp.BlockNonPublicDial (SSRF
	// guard) + standard TLS verification. When this smoke test is run from a dev
	// box sitting behind a fake-IP proxy/TUN (clash/mihomo/sing-box map the
	// panel host into 198.18.0.0/15), the guard correctly refuses the
	// special-use address and the proxy may also MITM the cert — both are local
	// artifacts, not the behaviour we're testing. Bearer-token mode needs only
	// baseURL + apiToken.
	c := &Client{
		baseURL:  strings.TrimRight(base, "/"),
		apiToken: token,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // local smoke test only
		},
	}
	ctx := context.Background()

	inbounds, err := c.ListInbounds(ctx)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) < 2 {
		t.Skipf("need >=2 inbounds for the multi-inbound test, panel has %d", len(inbounds))
	}
	a, b := inbounds[0].ID, inbounds[1].ID

	const email = "psp-livetest@psp.local"
	// Pre-clean any leftover from a previous aborted run, then guarantee teardown.
	_ = c.DelClientByEmail(ctx, a, email)
	t.Cleanup(func() { _ = c.DelClientByEmail(ctx, a, email) })

	// 1. Create one client attached to BOTH inbounds in a single call.
	if err := c.AddClientToInbounds(ctx, []int{a, b}, ports.ClientSpec{Email: email, Enable: true}); err != nil {
		t.Fatalf("AddClientToInbounds: %v", err)
	}
	assertAttached(t, c, ctx, email, a, b)

	// 2. Detach from b → only a remains.
	if err := c.DetachClient(ctx, email, []int{b}); err != nil {
		t.Fatalf("DetachClient: %v", err)
	}
	assertAttached(t, c, ctx, email, a)

	// 3. Re-attach b → both again.
	if err := c.AttachClient(ctx, email, []int{b}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	assertAttached(t, c, ctx, email, a, b)
}

// TestLive_SharedClientMigrationFlow mirrors what the v3.9.0 migration does to a
// real panel: create the shared client with the actual SILENT spec (id=uuid,
// password=uuid, auth=uuid — what buildSharedClientSpec produces for the default
// class) across two inbounds, confirm the attach, then delete it (the per-node
// cleanup). Verifies the real migration spec is accepted + fully removable.
func TestLive_SharedClientMigrationFlow(t *testing.T) {
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	if base == "" || token == "" {
		t.Skip("set PSP_LIVE_XUI_URL and PSP_LIVE_XUI_TOKEN to run the live 3X-UI smoke test")
	}
	c := &Client{
		baseURL:  strings.TrimRight(base, "/"),
		apiToken: token,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // local smoke test only
		},
	}
	ctx := context.Background()
	inbounds, err := c.ListInbounds(ctx)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) < 2 {
		t.Skipf("need >=2 inbounds, panel has %d", len(inbounds))
	}
	a, b := inbounds[0].ID, inbounds[1].ID

	const email = "psp-migtest@psp.local"
	const uuid = "11111111-2222-3333-4444-555555555555"
	_ = c.DelClientByEmail(ctx, a, email)
	t.Cleanup(func() { _ = c.DelClientByEmail(ctx, a, email) })

	// Create with the silent migration spec (all per-protocol fields populated).
	spec := ports.ClientSpec{Email: email, Enable: true, ID: uuid, Password: uuid, Auth: uuid}
	if err := c.AddClientToInbounds(ctx, []int{a, b}, spec); err != nil {
		t.Fatalf("AddClientToInbounds (silent spec): %v", err)
	}
	assertAttached(t, c, ctx, email, a, b)

	// Delete (the migration's legacy cleanup) → the client is fully gone.
	if err := c.DelClientByEmail(ctx, a, email); err != nil {
		t.Fatalf("DelClientByEmail: %v", err)
	}
	if cd, err := c.GetClient(ctx, email); err != nil {
		t.Fatalf("GetClient after delete: %v", err)
	} else if cd != nil {
		t.Fatalf("client must be gone after DelClientByEmail, got inboundIds=%v", cd.InboundIDs)
	}
}

// TestLive_BulkDelPreservesSharedClient validates the v3.9.0 migration-cleanup
// safety claim on a real panel: DeleteLegacyForUser removes the legacy per-node
// clients (emails u{uid}-n{id}@domain) with one panel-wide BulkDelByEmail, and the
// shared client (a DISTINCT email u{uid}@domain) must SURVIVE — proving the
// email-keyed bulk delete never nukes the just-provisioned shared client.
func TestLive_BulkDelPreservesSharedClient(t *testing.T) {
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	if base == "" || token == "" {
		t.Skip("set PSP_LIVE_XUI_URL and PSP_LIVE_XUI_TOKEN to run the live 3X-UI smoke test")
	}
	c := &Client{
		baseURL:  strings.TrimRight(base, "/"),
		apiToken: token,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // local smoke test only
		},
	}
	ctx := context.Background()
	inbounds, err := c.ListInbounds(ctx)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) < 2 {
		t.Skipf("need >=2 inbounds, panel has %d", len(inbounds))
	}
	a, b := inbounds[0].ID, inbounds[1].ID

	const shared = "u9001@psp.local"     // shared client (survives)
	const legacy1 = "u9001-n1@psp.local" // legacy per-node (deleted)
	const legacy2 = "u9001-n2@psp.local" // legacy per-node (deleted)
	for _, e := range []string{shared, legacy1, legacy2} {
		_ = c.DelClientByEmail(ctx, a, e)
		_ = c.DelClientByEmail(ctx, b, e)
	}
	t.Cleanup(func() {
		for _, e := range []string{shared, legacy1, legacy2} {
			_ = c.DelClientByEmail(ctx, a, e)
			_ = c.DelClientByEmail(ctx, b, e)
		}
	})

	// Shared client on both inbounds; one legacy client per inbound.
	if err := c.AddClientToInbounds(ctx, []int{a, b}, ports.ClientSpec{Email: shared, Enable: true, ID: "10000000-0000-0000-0000-000000000001"}); err != nil {
		t.Fatalf("add shared: %v", err)
	}
	if err := c.AddClientToInbounds(ctx, []int{a}, ports.ClientSpec{Email: legacy1, Enable: true, ID: "10000000-0000-0000-0000-000000000002"}); err != nil {
		t.Fatalf("add legacy1: %v", err)
	}
	if err := c.AddClientToInbounds(ctx, []int{b}, ports.ClientSpec{Email: legacy2, Enable: true, ID: "10000000-0000-0000-0000-000000000003"}); err != nil {
		t.Fatalf("add legacy2: %v", err)
	}

	// Batch-delete ONLY the legacy emails (what DeleteLegacyForUser does per panel).
	if _, err := c.BulkDelByEmail(ctx, []string{legacy1, legacy2}); err != nil {
		t.Fatalf("BulkDelByEmail: %v", err)
	}

	// The shared client must survive, still attached to both inbounds...
	assertAttached(t, c, ctx, shared, a, b)
	// ...while both legacy clients are gone.
	for _, e := range []string{legacy1, legacy2} {
		if cd, err := c.GetClient(ctx, e); err != nil {
			t.Fatalf("GetClient(%s): %v", e, err)
		} else if cd != nil {
			t.Fatalf("legacy client %s must be gone after bulk delete, got %v", e, cd.InboundIDs)
		}
	}
}

// TestLive_ConcurrentSameClientNoCorruption pins the v3.9.0-beta.5 fix for the
// real-panel migration failure: several flows (the boot heal, the per-user migrate
// task, the 2-min traffic-poll lifecycle push) all mutate the SAME shared client at
// once. 3X-UI's client endpoints reject concurrent same-client writes with "email
// already in use" or "UNIQUE constraint failed: client_inbounds.client_id,
// client_inbounds.inbound_id" (and the rolled-back update silently drops the
// enable/expiry change). The per-email write lock must serialize them so NO
// client_inbounds corruption occurs and the client ends single + updatable. Losers
// racing the create may still get a clean "email already in use" — fine (PSP's
// GetClient-first skip + task retry converge); only client_inbounds is a failure.
func TestLive_ConcurrentSameClientNoCorruption(t *testing.T) {
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	if base == "" || token == "" {
		t.Skip("set PSP_LIVE_XUI_URL and PSP_LIVE_XUI_TOKEN to run the live 3X-UI smoke test")
	}
	c := &Client{
		baseURL:  strings.TrimRight(base, "/"),
		apiToken: token,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // local smoke test only
		},
	}
	ctx := context.Background()
	inbounds, err := c.ListInbounds(ctx)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) < 2 {
		t.Skipf("need >=2 inbounds, panel has %d", len(inbounds))
	}
	a, b := inbounds[0].ID, inbounds[1].ID
	const email = "psp-cc-livetest@psp.local"
	const uuid = "44444444-5555-6666-7777-888888888888"
	_ = c.DelClientByEmail(ctx, a, email)
	t.Cleanup(func() { _ = c.DelClientByEmail(ctx, a, email) })

	spec := ports.ClientSpec{Email: email, Enable: true, ID: uuid, Password: uuid, Auth: uuid}
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if e := c.AddClientToInbounds(ctx, []int{a, b}, spec); e != nil {
				errs[idx] = e
				return
			}
			u := spec
			u.Enable = idx%2 == 0
			errs[idx] = c.UpdateClient(ctx, 0, uuid, u)
		}(g)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil && strings.Contains(e.Error(), "client_inbounds") {
			t.Fatalf("goroutine %d hit client_inbounds corruption (serialization failed): %v", i, e)
		}
	}
	if cd, err := c.GetClient(ctx, email); err != nil || cd == nil {
		t.Fatalf("final GetClient: err=%v nil=%v", err, cd == nil)
	}
	if err := c.UpdateClient(ctx, 0, uuid, spec); err != nil {
		t.Fatalf("final UpdateClient must succeed after the concurrent storm, got: %v", err)
	}
}

func assertAttached(t *testing.T, c *Client, ctx context.Context, email string, want ...int) {
	t.Helper()
	cd, err := c.GetClient(ctx, email)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if cd == nil {
		t.Fatalf("GetClient(%s) = nil, want a client", email)
	}
	got := append([]int(nil), cd.InboundIDs...)
	sort.Ints(got)
	sort.Ints(want)
	if len(got) != len(want) {
		t.Fatalf("inboundIds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inboundIds = %v, want %v", got, want)
		}
	}
}
