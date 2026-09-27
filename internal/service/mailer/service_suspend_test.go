package mailer

import (
	"context"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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

// fakeSMTP is an SMTP server on loopback that accepts every message and counts
// what it delivered: just enough of the protocol for net/smtp's client, and no
// extension advertised, so the client never asks for STARTTLS, AUTH or
// 8BITMIME. A delivery is counted before the final 250 is sent, so the send
// has returned only after the count moved.
type fakeSMTP struct {
	ln        net.Listener
	mu        sync.Mutex
	delivered int
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // closed by Cleanup
			}
			go f.session(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) session(conn net.Conn) {
	defer conn.Close()
	tp := textproto.NewConn(conn)
	reply := func(line string) { _ = tp.PrintfLine("%s", line) }
	reply("220 fake.example.test ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		switch cmd := strings.ToUpper(line); {
		case cmd == "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			if _, err := tp.ReadDotBytes(); err != nil {
				return
			}
			f.mu.Lock()
			f.delivered++
			f.mu.Unlock()
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default: // EHLO, MAIL FROM, RCPT TO
			reply("250 ok")
		}
	}
}

func (f *fakeSMTP) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.delivered
}

// settings points the mailer at this server over plain TCP.
func (f *fakeSMTP) settings() domain.MailSettings {
	addr := f.ln.Addr().(*net.TCPAddr)
	return domain.MailSettings{
		Enabled: true, SMTPHost: addr.IP.String(), SMTPPort: addr.Port,
		FromEmail: "psp@example.test", Encryption: "none",
	}
}

// slotRepo is the mail repository as far as a service-suspension mail reaches
// it: the settings, no stored templates (so the shipped defaults apply), and
// ReserveSentSlot with the mail_sent table's semantics — the first reservation
// of a (user, kind, window) wins, every later one loses. reserved keeps every
// window key asked for, won or lost.
type slotRepo struct {
	ports.MailRepo
	settings domain.MailSettings
	slots    map[string]bool
	reserved []string
}

func (r *slotRepo) LoadSettings(context.Context, domain.MailSettings) (domain.MailSettings, error) {
	return r.settings, nil
}

func (r *slotRepo) ListTemplates(context.Context) ([]*domain.MailTemplate, error) { return nil, nil }

func (r *slotRepo) ReserveSentSlot(_ context.Context, userID int64, kind domain.MailReminderKind, windowKey, _ string) (bool, error) {
	r.reserved = append(r.reserved, windowKey)
	slot := fmt.Sprintf("%d|%s|%s", userID, kind, windowKey)
	if r.slots[slot] {
		return false, nil
	}
	r.slots[slot] = true
	return true, nil
}

// defaultSettingsOnly resolves every scope to the defaults it is handed.
type defaultSettingsOnly struct{}

func (defaultSettingsOnly) Load(_ context.Context, d ports.UISettings) (ports.UISettings, error) {
	return d, nil
}
func (defaultSettingsOnly) LoadForGroup(_ context.Context, _ int64, d ports.UISettings) (ports.UISettings, error) {
	return d, nil
}
func (defaultSettingsOnly) LoadForUser(_ context.Context, _ *domain.User, d ports.UISettings) (ports.UISettings, error) {
	return d, nil
}

// The per-day bucket must be what the SEND path reserves, not only what the
// helper above returns: a send path back on eventWindowKey would mail a
// persistent sharer after every re-suspension all day, and the helper's own
// test would stay green. Two geo_auto suspensions of one user, one mail.
// Nothing here can move the clock between the two calls, so the "different
// minute" is carried by the key instead: both reserve the slot a suspension
// at 00:00 UTC today would have reserved — any minute of the day, one slot.
func TestSendServiceSuspendedNotification_GeoAutoMailsOncePerUTCDay(t *testing.T) {
	server := newFakeSMTP(t)
	repo := &slotRepo{settings: server.settings(), slots: map[string]bool{}}
	svc := New(repo, nil, nil, defaultSettingsOnly{}, nil)
	u := &domain.User{ID: 7, UPN: "sharer@example.test"}

	day := time.Now().UTC().Format("2006-01-02")
	for i := 1; i <= 2; i++ {
		if err := svc.SendServiceSuspendedNotification(context.Background(), u, string(domain.DisabledGeoAutoSuspend), "about 60 minutes"); err != nil {
			t.Fatalf("suspension %d: %v", i, err)
		}
	}
	if time.Now().UTC().Format("2006-01-02") != day {
		t.Skip("the two calls straddled UTC midnight, which is two days and rightly two mails")
	}

	if got := server.count(); got != 1 {
		t.Fatalf("mails delivered = %d, want 1 for two geo_auto suspensions in one UTC day", got)
	}
	midnight, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("parse %q: %v", day, err)
	}
	want := serviceSuspendWindowKey(string(domain.DisabledGeoAutoSuspend), midnight)
	if len(repo.reserved) != 2 || repo.reserved[0] != want || repo.reserved[1] != want {
		t.Fatalf("reserved slots = %q, want %q for both — the slot every minute of today shares", repo.reserved, want)
	}
}
