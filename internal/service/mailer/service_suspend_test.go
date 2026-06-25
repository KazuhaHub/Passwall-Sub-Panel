package mailer

import (
	"strings"
	"testing"

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
		"":                                     "服务暂停",
		"something_unknown":                    "something_unknown", // falls back to the raw code
	}
	for in, want := range cases {
		if got := serviceSuspendReasonText(in); got != want {
			t.Errorf("serviceSuspendReasonText(%q) = %q, want %q", in, got, want)
		}
	}
}
