package xui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// TestLive_XUITrafficFloorMatrix is the regression guard for the traffic-floor
// datum fix (docs/traffic-floor-defect.md). PSP computes quota headroom per
// PERIOD; the panel enforces against a lifetime counter it never resets. Before
// the fix, pushing the bare headroom disabled a user at half their quota and
// then re-disabled them within one sweep after every re-enable.
//
// Every case below drives PSP's real adapter with a cap built the way the push
// path now builds it — domain.PanelQuotaCap(headroom, panelLifetime) — and
// asserts the panel leaves the client alone until the quota is genuinely gone.
//
// Env-gated, so CI skips it. Needs a scratch panel with a live xray, since the
// depletion sweep is gated on IsXrayRunning, plus direct access to the panel's
// SQLite file: 3X-UI exposes no API for seeding accumulated usage, which is
// what upstream's own tests do via seedClientRow.
func TestLive_XUITrafficFloorMatrix(t *testing.T) {
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	dbPath := os.Getenv("PSP_LIVE_XUI_DB")
	if base == "" || token == "" || dbPath == "" {
		t.Skip("set PSP_LIVE_XUI_URL, PSP_LIVE_XUI_TOKEN and PSP_LIVE_XUI_DB")
	}
	jar, _ := cookiejar.New(nil)
	c := &Client{
		baseURL: strings.TrimRight(base, "/"), apiToken: token,
		http: &http.Client{Timeout: 30 * time.Second, Jar: jar}, jar: jar,
	}
	ctx := context.Background()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open panel db: %v", err)
	}
	defer db.Close()

	const GB = int64(1) << 30
	const limit = 100 * GB

	settings, _ := json.Marshal(map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}})
	stream, _ := json.Marshal(map[string]any{"network": "tcp", "security": "none"})
	sniffing, _ := json.Marshal(map[string]any{"enabled": false, "destOverride": []string{"http", "tls"}})
	stamp := time.Now().UnixNano()
	inbID, err := c.AddInbound(ctx, ports.InboundSpec{
		Remark: fmt.Sprintf("psp-floormatrix-%d", stamp), Enable: false, Port: scratchPort(stamp),
		Protocol: "vless", Settings: string(settings),
		StreamSettings: string(stream), Sniffing: string(sniffing),
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	t.Cleanup(func() { _ = c.DelInbound(context.Background(), inbID) })

	// waitDisabled reports whether the panel's depletion sweep (@every 5s)
	// disables the client within the window.
	waitDisabled := func(email string, window time.Duration) bool {
		deadline := time.Now().Add(window)
		for time.Now().Before(deadline) {
			var e bool
			if err := db.QueryRow(`SELECT enable FROM client_traffics WHERE email = ?`, email).Scan(&e); err != nil {
				t.Fatalf("poll enable: %v", err)
			}
			if !e {
				return true
			}
			time.Sleep(1500 * time.Millisecond)
		}
		return false
	}

	probe := func(t *testing.T, name string, lifetime, periodUsed int64) (string, string, bool) {
		t.Helper()
		email := fmt.Sprintf("u%d-%s@psp.local", stamp, name)
		uuid := fmt.Sprintf("%08x-0000-4000-8000-%012d", stamp&0xffffffff, len(name)*1000+int(periodUsed/GB))
		if err := c.AddClientToInbounds(ctx, []int{inbID}, ports.ClientSpec{
			Email: email, ID: uuid, Enable: true,
		}); err != nil {
			t.Fatalf("AddClientToInbounds: %v", err)
		}
		t.Cleanup(func() { _, _ = c.BulkDelByEmail(context.Background(), []string{email}) })

		// Seed the accumulated usage xray would have reported BEFORE pushing,
		// so the cap is built against the same counter the panel will compare
		// it to — the ordering the traffic poll itself has (read, then push).
		if _, err := db.Exec(`UPDATE client_traffics SET up = ?, down = ? WHERE email = ?`,
			lifetime/2, lifetime-lifetime/2, email); err != nil {
			t.Fatalf("seed usage: %v", err)
		}
		cap := domain.PanelQuotaCap(user.TrafficFloorBytes(limit, periodUsed), lifetime)
		if err := c.UpdateClient(ctx, ports.ClientSpec{
			Email: email, ID: uuid, Enable: true, TotalGB: cap,
		}); err != nil {
			t.Fatalf("UpdateClient: %v", err)
		}
		disabled := waitDisabled(email, 20*time.Second)
		t.Logf("lifetime=%3dGB periodUsed=%3dGB (quota %3d%%) -> PSP pushes totalGB=%3dGB -> disabled=%v",
			lifetime/GB, periodUsed/GB, 100*periodUsed/limit, cap/GB, disabled)
		return email, uuid, disabled
	}

	// --- Half quota is where the defect used to trip -----------------------
	t.Run("first period, past half quota", func(t *testing.T) {
		if _, _, disabled := probe(t, "p55", 55*GB, 55*GB); disabled {
			t.Error("disabled at 55% of quota — the period/lifetime mismatch is back")
		}
	})
	t.Run("first period, deep into quota", func(t *testing.T) {
		if _, _, disabled := probe(t, "p90", 90*GB, 90*GB); disabled {
			t.Error("disabled at 90% of quota — a user with 10GB left must stay online")
		}
	})

	// --- An aged client on a fresh period --------------------------------
	// The worst pre-fix case: two periods of history, nothing used yet this
	// period. The cap has to sit above the client's whole lifetime, not at
	// the bare quota.
	t.Run("fresh period, zero used, aged client", func(t *testing.T) {
		if _, _, disabled := probe(t, "aged", 200*GB, 0); disabled {
			t.Error("an aged client with a FULL fresh quota was disabled")
		}
	})

	// --- Enforcement must still actually happen --------------------------
	// The fix must not turn the safety net off. Note what "cut" means after
	// rebasing: TrafficFloorBytes returns its exhausted sentinel (1) and the
	// cap becomes lifetime+1, so the panel cuts as soon as the client moves
	// ANY further traffic — not while it sits idle at exactly its limit.
	//
	// That is the correct shape for a net whose job is bounding abuse during
	// a PSP outage: an idle exhausted user is consuming nothing, and the
	// moment they consume anything they are gone, within one traffic tick
	// (@every 5s). The pre-fix behaviour cut an idle user too, but only as a
	// side effect of pushing a cap of 1 byte, which also cut everyone else.
	t.Run("an exhausted user is cut the moment they move traffic", func(t *testing.T) {
		email, _, disabled := probe(t, "spent", 100*GB, 100*GB)
		if disabled {
			t.Log("cut while idle at exactly the limit")
			return
		}
		// The user sends something. One traffic tick later they must be gone.
		if _, err := db.Exec(`UPDATE client_traffics SET up = up + ? WHERE email = ?`,
			64<<10, email); err != nil {
			t.Fatalf("advance usage: %v", err)
		}
		if !waitDisabled(email, 20*time.Second) {
			t.Error("an exhausted user kept going after moving more traffic — the floor no longer enforces")
		}
	})

	// --- The pushed cap must be STABLE as traffic accrues ----------------
	// The panel counter grows by the same delta the period headroom shrinks
	// by, so the rebased cap is invariant. That is what lets the push path's
	// no-op skip finally fire for an active user, and it is a property of the
	// real panel round-trip, not just of the arithmetic.
	t.Run("the pushed cap does not move as traffic accrues", func(t *testing.T) {
		email, uuid, _ := probe(t, "stable", 10*GB, 10*GB)
		var first int64
		for cycle := 0; cycle < 4; cycle++ {
			lifetime := (10 + int64(cycle)*7) * GB
			if _, err := db.Exec(`UPDATE client_traffics SET up = ?, down = ? WHERE email = ?`,
				lifetime/2, lifetime-lifetime/2, email); err != nil {
				t.Fatalf("advance usage: %v", err)
			}
			cap := domain.PanelQuotaCap(user.TrafficFloorBytes(limit, lifetime), lifetime)
			if err := c.UpdateClient(ctx, ports.ClientSpec{
				Email: email, ID: uuid, Enable: true, TotalGB: cap,
			}); err != nil {
				t.Fatalf("cycle %d push: %v", cycle, err)
			}
			got, err := c.GetClient(ctx, email)
			if err != nil || got == nil {
				t.Fatalf("cycle %d GetClient: %v", cycle, err)
			}
			t.Logf("cycle %d: lifetime=%dGB -> panel holds totalGB=%dGB", cycle, lifetime/GB, got.TotalGB/GB)
			if cycle == 0 {
				first = got.TotalGB
				continue
			}
			if got.TotalGB != first {
				t.Errorf("cycle %d: panel holds %d, want it unchanged at %d", cycle, got.TotalGB, first)
			}
		}
		if waitDisabled(email, 10*time.Second) {
			t.Error("client was disabled while still well inside its quota")
		}
	})
}
