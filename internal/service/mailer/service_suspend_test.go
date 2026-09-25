package mailer

import (
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The new service-suspend / -restore templates must exist, be enabled, and
// render with the placeholders the send path supplies — including surfacing the
// suspension reason (the whole point of the feature).
func TestDefaultTemplates_ServiceSuspendRestore(t *testing.T) {
	byKind := map[domain.MailReminderKind]*domain.MailTemplate{}
	for _, tpl := range DefaultTemplates() {
		byKind[tpl.Kind] = tpl
	}

	susp := byKind[domain.MailReminderServiceSuspended]
	if susp == nil || !susp.Enabled {
		t.Fatal("service_suspended template missing or disabled in DefaultTemplates")
	}
	data := map[string]any{
		"UPN":           "u@example.test",
		"SuspendReason": "使用了被禁止的客户端",
		"SuspendDetail": "auto-disabled after 5 violations",
		"GeneratedAt":   "2026-06-25 12:00",
	}
	body, err := renderHTMLTemplate("susp", susp.Body, data)
	if err != nil {
		t.Fatalf("service_suspended body render: %v", err)
	}
	if !strings.Contains(body, "使用了被禁止的客户端") {
		t.Fatalf("suspend body must surface the reason, got: %s", body)
	}
	if !strings.Contains(body, "auto-disabled after 5 violations") {
		t.Fatalf("suspend body must surface the detail, got: %s", body)
	}

	restored := byKind[domain.MailReminderServiceRestored]
	if restored == nil || !restored.Enabled {
		t.Fatal("service_restored template missing or disabled in DefaultTemplates")
	}
	if _, err := renderHTMLTemplate("rest", restored.Body, map[string]any{"UPN": "u@example.test", "GeneratedAt": "2026-06-25 12:00"}); err != nil {
		t.Fatalf("service_restored body render: %v", err)
	}

	for _, tpl := range []*domain.MailTemplate{susp, restored} {
		if _, err := renderTemplate("subj", tpl.Subject, data); err != nil {
			t.Fatalf("subject render for %s: %v", tpl.Kind, err)
		}
	}
}

func TestServiceSuspendReasonText(t *testing.T) {
	cases := map[string]string{
		string(domain.DisabledTrafficExceeded): "本期流量已用完",
		string(domain.DisabledBlockedClient):   "使用了被禁止的客户端",
		string(domain.DisabledServiceManual):   "管理员手动暂停服务",
		string(domain.DisabledExpired):         "订阅已到期",
		// The detector's own suspension says what was observed and that it
		// ends by itself — not that a person decided, which is geo_anomaly's
		// wording and would be false here.
		string(domain.DisabledGeoAutoSuspend): "检测到账号在多个地区同时使用，代理服务已临时暂停，到时会自动恢复；如有疑问请联系管理员",
		"":                                    "服务暂停",
		"something_unknown":                   "something_unknown", // falls back to the raw code
	}
	for in, want := range cases {
		if got := serviceSuspendReasonText(in); got != want {
			t.Errorf("serviceSuspendReasonText(%q) = %q, want %q", in, got, want)
		}
	}
}

// A persistent sharer is re-suspended soon after every automatic lift, all day.
// With the minute bucket every other reason uses, each re-suspension mailed
// again. geo_auto dedups per user per UTC day instead: the first mail already
// says service returns by itself. Every other reason keeps the minute bucket,
// so a real later state change still re-notifies.
func TestServiceSuspendWindowKey_GeoAutoIsPerDay(t *testing.T) {
	now := time.Date(2026, 9, 24, 23, 59, 30, 0, time.FixedZone("UTC+8", 8*3600))
	if got, want := serviceSuspendWindowKey(string(domain.DisabledGeoAutoSuspend), now), "geo_auto@2026-09-24"; got != want {
		t.Fatalf("geo_auto key = %q, want %q", got, want)
	}
	later := now.Add(6 * time.Hour) // same UTC day, a different local day
	if a, b := serviceSuspendWindowKey(string(domain.DisabledGeoAutoSuspend), now), serviceSuspendWindowKey(string(domain.DisabledGeoAutoSuspend), later); a != b {
		t.Fatalf("keys %q and %q differ within one UTC day", a, b)
	}
	if got, want := serviceSuspendWindowKey(string(domain.DisabledBlockedClient), now), "blocked_client@2026-09-24T15:59"; got != want {
		t.Fatalf("blocked_client key = %q, want %q (minute bucket, UTC)", got, want)
	}
}
