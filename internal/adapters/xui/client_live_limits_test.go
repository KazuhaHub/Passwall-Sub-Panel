package xui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestLive_XUIConnectionLimits verifies that the per-user connection caps PSP
// now owns actually land on, and survive on, a real panel.
//
// The device cap is the one that needs live proof. PSP used to omit limitHwid
// deliberately, and every update therefore reset it to 0 — which silently
// disabled enforcement, since the panel allows everything at a cap of 0. Now
// that PSP owns the field the value is intent rather than an echo, and the
// property that matters is that a SUBSEQUENT unrelated update does not wipe it.
//
//	PSP_LIVE_XUI_URL='http://127.0.0.1:54999' \
//	PSP_LIVE_XUI_TOKEN='<admin-scope api token>' \
//	  go test ./internal/adapters/xui/ -run TestLive_XUIConnectionLimits -v
//
// scratchPort spreads scratch inbounds over a recognisable range keyed by the
// run stamp. A fixed port meant one aborted run left an inbound behind that
// blocked every rerun with "port already used" — the tests clean up after
// themselves, but only when they get far enough to register the cleanup.
// Inbounds are created disabled and never bind, so collisions are only a
// bookkeeping problem, not a real port conflict.
func scratchPort(stamp int64) int {
	return 39100 + int((stamp/1e6)%100)
}

func TestLive_XUIConnectionLimits(t *testing.T) {
	base := os.Getenv("PSP_LIVE_XUI_URL")
	token := os.Getenv("PSP_LIVE_XUI_TOKEN")
	if base == "" || token == "" {
		t.Skip("set PSP_LIVE_XUI_URL and PSP_LIVE_XUI_TOKEN to run the connection-limit test")
	}
	jar, _ := cookiejar.New(nil)
	c := &Client{
		baseURL: strings.TrimRight(base, "/"), apiToken: token,
		http: &http.Client{Timeout: 30 * time.Second, Jar: jar}, jar: jar,
	}
	ctx := context.Background()

	settings, _ := json.Marshal(map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}})
	stream, _ := json.Marshal(map[string]any{"network": "tcp", "security": "none"})
	sniffing, _ := json.Marshal(map[string]any{"enabled": false, "destOverride": []string{"http", "tls"}})
	stamp := time.Now().UnixNano()
	inbID, err := c.AddInbound(ctx, ports.InboundSpec{
		Remark: fmt.Sprintf("psp-limits-%d", stamp), Enable: false, Port: scratchPort(stamp),
		Protocol: "vless", Settings: string(settings),
		StreamSettings: string(stream), Sniffing: string(sniffing),
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	t.Cleanup(func() { _ = c.DelInbound(context.Background(), inbID) })

	email := fmt.Sprintf("u%d-limits@psp.local", stamp)
	uuid := fmt.Sprintf("%08x-0000-4000-8000-000000000009", stamp&0xffffffff)
	if err := c.AddClientToInbounds(ctx, []int{inbID}, ports.ClientSpec{
		Email: email, ID: uuid, Enable: true, LimitIP: 3, LimitHwid: 2,
	}); err != nil {
		t.Fatalf("AddClientToInbounds: %v", err)
	}
	t.Cleanup(func() { _, _ = c.BulkDelByEmail(context.Background(), []string{email}) })

	t.Run("create carries both caps", func(t *testing.T) {
		got, err := c.GetClient(ctx, email)
		if err != nil || got == nil {
			t.Fatalf("GetClient: %v", err)
		}
		if got.LimitIP != 3 {
			t.Errorf("limitIp = %d, want 3", got.LimitIP)
		}
		if got.LimitHwid != 2 {
			t.Errorf("limitHwid = %d, want 2", got.LimitHwid)
		}
	})

	t.Run("update carries both caps", func(t *testing.T) {
		if err := c.UpdateClient(ctx, ports.ClientSpec{
			Email: email, ID: uuid, Enable: true, LimitIP: 5, LimitHwid: 4,
		}); err != nil {
			t.Fatalf("UpdateClient: %v", err)
		}
		got, err := c.GetClient(ctx, email)
		if err != nil || got == nil {
			t.Fatalf("GetClient: %v", err)
		}
		if got.LimitIP != 5 || got.LimitHwid != 4 {
			t.Errorf("caps after update = %d/%d, want 5/4", got.LimitIP, got.LimitHwid)
		}
	})

	// The regression this feature exists to close: before PSP owned limitHwid,
	// a push that cared about something else entirely reset it to 0 and turned
	// device enforcement off without saying anything.
	t.Run("an unrelated update does not wipe the device cap", func(t *testing.T) {
		if err := c.UpdateClient(ctx, ports.ClientSpec{
			Email: email, ID: uuid, Enable: true, LimitIP: 5, LimitHwid: 4,
			ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli(),
		}); err != nil {
			t.Fatalf("UpdateClient(expiry): %v", err)
		}
		got, err := c.GetClient(ctx, email)
		if err != nil || got == nil {
			t.Fatalf("GetClient: %v", err)
		}
		if got.LimitHwid != 4 {
			t.Errorf("limitHwid = %d after an expiry-only change, want 4 — the wipe is back", got.LimitHwid)
		}
		if got.LimitIP != 5 {
			t.Errorf("limitIp = %d, want 5", got.LimitIP)
		}
	})

	// 0 has to mean "no cap" end to end, or an unlimited user inherits one.
	t.Run("zero clears both caps", func(t *testing.T) {
		if err := c.UpdateClient(ctx, ports.ClientSpec{
			Email: email, ID: uuid, Enable: true,
		}); err != nil {
			t.Fatalf("UpdateClient(clear): %v", err)
		}
		got, err := c.GetClient(ctx, email)
		if err != nil || got == nil {
			t.Fatalf("GetClient: %v", err)
		}
		if got.LimitIP != 0 || got.LimitHwid != 0 {
			t.Errorf("caps after clear = %d/%d, want 0/0", got.LimitIP, got.LimitHwid)
		}
	})
}
