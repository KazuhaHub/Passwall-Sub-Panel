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

// STATUS: this test FAILS against a live panel today, on purpose. The defect it
// probes is real and unfixed — see docs/traffic-floor-defect.md. It is env-gated
// so CI skips it; when the floor is fixed, the failing branch below becomes the
// regression guard.
//
// TestLive_XUITrafficFloorSemantics answers one question against a real panel:
// when PSP pushes TrafficFloorBytes(limit, periodUsed) into a client's totalGB,
// what does the panel actually compare that number against?
//
// PSP's model assumes the panel measures the CURRENT PERIOD. If the panel
// instead measures the client's LIFETIME counter, the floor fires early — and
// gets steadily worse as the client ages, because PSP never resets the panel's
// counter (it keeps its own period baselines) and pins the panel's own reset
// cycle to "never".
//
// Requires a scratch panel with a live xray (the depletion sweep is gated on
// IsXrayRunning) and direct read/write access to its SQLite file, since 3X-UI
// exposes no API for seeding a client's accumulated usage — the same thing
// upstream's own tests do via seedClientRow.
//
//	PSP_LIVE_XUI_URL='http://127.0.0.1:54999' \
//	PSP_LIVE_XUI_TOKEN='<admin-scope api token>' \
//	PSP_LIVE_XUI_DB='/path/to/x-ui.db' \
//	  go test ./internal/adapters/xui/ -run TestLive_XUITrafficFloorSemantics -v
func TestLive_XUITrafficFloorSemantics(t *testing.T) {
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	dbPath := os.Getenv("PSP_LIVE_XUI_DB")
	if base == "" || token == "" || dbPath == "" {
		t.Skip("set PSP_LIVE_XUI_URL, PSP_LIVE_XUI_TOKEN and PSP_LIVE_XUI_DB to run the traffic-floor semantics test")
	}
	jar, _ := cookiejar.New(nil)
	c := &Client{
		baseURL:  strings.TrimRight(base, "/"),
		apiToken: token,
		http:     &http.Client{Timeout: 30 * time.Second, Jar: jar},
		jar:      jar,
	}
	ctx := context.Background()

	const GB = int64(1) << 30
	const (
		limit      = 100 * GB
		periodUsed = 60 * GB // the user is at 60% of their monthly quota
	)
	// Lifetime equals period usage here, so this is the FIRST period of a brand
	// new client — the most favourable case for PSP's model. If the floor
	// misfires even here, every later period is strictly worse.
	lifetimeUp, lifetimeDown := 30*GB, 30*GB

	settings, _ := json.Marshal(map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}})
	stream, _ := json.Marshal(map[string]any{"network": "tcp", "security": "none"})
	sniffing, _ := json.Marshal(map[string]any{"enabled": false, "destOverride": []string{"http", "tls"}})

	stamp := time.Now().UnixNano()
	inbID, err := c.AddInbound(ctx, ports.InboundSpec{
		Remark: fmt.Sprintf("psp-floor-%d", stamp), Enable: false, Port: 39101,
		Protocol: "vless", Settings: string(settings),
		StreamSettings: string(stream), Sniffing: string(sniffing),
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	t.Cleanup(func() { _ = c.DelInbound(context.Background(), inbID) })

	email := fmt.Sprintf("u%d@psp.local", stamp)
	uuid := fmt.Sprintf("%08x-0000-4000-8000-000000000001", stamp&0xffffffff)
	if err := c.AddClientToInbounds(ctx, []int{inbID}, ports.ClientSpec{
		Email: email, ID: uuid, Enable: true, Flow: "",
	}); err != nil {
		t.Fatalf("AddClientToInbounds: %v", err)
	}
	t.Cleanup(func() { _, _ = c.BulkDelByEmail(context.Background(), []string{email}) })

	// Exactly what the traffic poll pushes every cycle for an active user.
	floor := user.TrafficFloorBytes(limit, periodUsed)
	if err := c.UpdateClient(ctx, inbID, uuid, ports.ClientSpec{
		Email: email, ID: uuid, Enable: true, TotalGB: floor,
	}); err != nil {
		t.Fatalf("UpdateClient(floor): %v", err)
	}
	t.Logf("pushed floor: limit=%dGB periodUsed=%dGB -> totalGB=%dGB", limit/GB, periodUsed/GB, floor/GB)

	if got, err := c.GetClient(ctx, email); err != nil {
		t.Fatalf("GetClient after floor push: %v", err)
	} else if got == nil || got.TotalGB != floor {
		t.Fatalf("floor did not persist: %+v", got)
	}

	// Seed the accumulated usage xray would have reported. No API exists for
	// this; upstream's own tests seed client_traffics the same way.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open panel db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE client_traffics SET up = ?, down = ? WHERE email = ?`,
		lifetimeUp, lifetimeDown, email); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	var up, down, total int64
	var enable bool
	row := db.QueryRow(`SELECT up, down, total, enable FROM client_traffics WHERE email = ?`, email)
	if err := row.Scan(&up, &down, &total, &enable); err != nil {
		t.Fatalf("read back seeded row: %v", err)
	}
	t.Logf("panel row before sweep: up+down=%dGB total=%dGB enable=%v", (up+down)/GB, total/GB, enable)
	if up+down != lifetimeUp+lifetimeDown || total != floor {
		t.Fatalf("seed did not land: up+down=%d total=%d", up+down, total)
	}

	// The depletion sweep runs on the panel's traffic cron (@every 5s).
	deadline := time.Now().Add(45 * time.Second)
	var disabled bool
	for time.Now().Before(deadline) {
		var e bool
		if err := db.QueryRow(`SELECT enable FROM client_traffics WHERE email = ?`, email).Scan(&e); err != nil {
			t.Fatalf("poll enable: %v", err)
		}
		if !e {
			disabled = true
			break
		}
		time.Sleep(2 * time.Second)
	}

	detail, derr := c.GetClient(ctx, email)
	if derr != nil {
		t.Fatalf("GetClient after sweep: %v", derr)
	}
	t.Logf("after sweep: client_traffics.enable=%v settings.enable=%v", !disabled, detail != nil && detail.Enable)

	if disabled {
		t.Errorf("FLOOR MISFIRE CONFIRMED: a user at %d%% of quota was disabled by the panel. "+
			"PSP pushed totalGB=%dGB (period remaining) and the panel compared it against the "+
			"client's %dGB LIFETIME counter.",
			100*periodUsed/limit, floor/GB, (lifetimeUp+lifetimeDown)/GB)
	} else {
		t.Logf("no misfire: the panel left the client enabled, so it is not comparing totalGB " +
			"against the lifetime counter the way the static reading predicted")
	}
}
