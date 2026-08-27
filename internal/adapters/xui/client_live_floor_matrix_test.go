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

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// STATUS: the flap subtest FAILS against a live panel today, on purpose — see
// docs/traffic-floor-defect.md. Env-gated, so CI skips it.
//
// TestLive_XUITrafficFloorMatrix maps the blast radius of the floor mismatch:
// where the trip point actually sits, and what happens after the poll's next
// push tries to undo it.
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
		Remark: fmt.Sprintf("psp-floormatrix-%d", stamp), Enable: false, Port: 39102,
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

		floor := user.TrafficFloorBytes(limit, periodUsed)
		if err := c.UpdateClient(ctx, inbID, uuid, ports.ClientSpec{
			Email: email, ID: uuid, Enable: true, TotalGB: floor,
		}); err != nil {
			t.Fatalf("UpdateClient: %v", err)
		}
		if _, err := db.Exec(`UPDATE client_traffics SET up = ?, down = ? WHERE email = ?`,
			lifetime/2, lifetime-lifetime/2, email); err != nil {
			t.Fatalf("seed usage: %v", err)
		}
		disabled := waitDisabled(email, 20*time.Second)
		t.Logf("lifetime=%3dGB periodUsed=%3dGB (quota %3d%%) -> PSP pushes totalGB=%3dGB -> disabled=%v",
			lifetime/GB, periodUsed/GB, 100*periodUsed/limit, floor/GB, disabled)
		return email, uuid, disabled
	}

	// --- Where does the trip point actually sit? -------------------------
	// In a client's FIRST period lifetime == periodUsed, so the panel's
	// condition (lifetime >= limit - periodUsed) collapses to
	// periodUsed >= limit/2. Bracket that prediction.
	t.Run("first period, below half quota", func(t *testing.T) {
		if _, _, disabled := probe(t, "p45", 45*GB, 45*GB); disabled {
			t.Error("disabled at 45% of quota — trip point is even earlier than limit/2")
		}
	})
	t.Run("first period, just past half quota", func(t *testing.T) {
		if _, _, disabled := probe(t, "p55", 55*GB, 55*GB); !disabled {
			t.Error("NOT disabled at 55% — the trip point is not limit/2")
		}
	})

	// --- The case with no usage at all this period -----------------------
	// A returning user on day 1 of a fresh billing period: PSP pushes the FULL
	// quota as the floor, but the lifetime counter carries every prior period.
	t.Run("fresh period, zero used, aged client", func(t *testing.T) {
		if _, _, disabled := probe(t, "aged", 200*GB, 0); !disabled {
			t.Error("an aged client was NOT disabled on a fresh period — good, but unexpected")
		}
	})

	// --- Does the poll's next push heal it, or does it flap? -------------
	t.Run("the next poll push does not heal it", func(t *testing.T) {
		email, uuid, disabled := probe(t, "flap", 70*GB, 70*GB)
		if !disabled {
			t.Fatal("precondition failed: client was not disabled")
		}
		// Exactly what SyncLifecycle sends on the next cycle: enable=true plus
		// a refreshed floor. If this heals, the damage is one cycle of outage;
		// if the panel re-disables, the user is down essentially always.
		floor := user.TrafficFloorBytes(limit, 70*GB)
		if err := c.UpdateClient(ctx, inbID, uuid, ports.ClientSpec{
			Email: email, ID: uuid, Enable: true, TotalGB: floor,
		}); err != nil {
			t.Fatalf("re-enable push: %v", err)
		}
		var e bool
		_ = db.QueryRow(`SELECT enable FROM client_traffics WHERE email = ?`, email).Scan(&e)
		t.Logf("immediately after PSP re-enables: enable=%v", e)

		if waitDisabled(email, 20*time.Second) {
			t.Errorf("FLAP CONFIRMED: PSP re-enables, the panel re-disables within one sweep. "+
				"The user is offline for all but a sliver of each %s poll cycle.", "5-minute")
		} else {
			t.Log("the re-enable stuck — damage is bounded to one poll cycle")
		}
	})
}
