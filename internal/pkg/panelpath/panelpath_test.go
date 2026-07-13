package panelpath

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type resolverSettingsRepo struct {
	settings ports.UISettings
	err      error
}

func (r *resolverSettingsRepo) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return r.settings, r.err
}

func (r *resolverSettingsRepo) Save(context.Context, ports.UISettings) error { return nil }

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"/", "", true},
		{"/panel", "/panel", true},
		{"/tools/passwall", "/tools/passwall", true},
		{"panel", "", false},
		{"/panel/", "", false},
		{"/a//b", "", false},
		{"/a/../b", "", false},
		{"/面板", "", false},
	} {
		got, err := Normalize(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Fatalf("Normalize(%q) = %q, %v; want %q, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestValidateAndURLs(t *testing.T) {
	if err := Validate("/panel", "sub"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api", "/health", "/sub", "/sub/portal"} {
		if err := Validate(p, "sub"); err == nil {
			t.Fatalf("Validate(%q) unexpectedly succeeded", p)
		}
	}
	if got := PanelURL("https://example.com", "/panel", "/reset-password?token=x"); got != "https://example.com/panel/reset-password?token=x" {
		t.Fatalf("PanelURL = %q", got)
	}
	if got := PanelBase("https://example.com/", "/panel"); got != "https://example.com/panel" {
		t.Fatalf("PanelBase = %q", got)
	}
	if _, err := PublicOrigin("https://example.com/old", true); err == nil {
		t.Fatal("mounted panel accepted a pathful origin")
	}
}

func TestResolverKeepsLastGoodSnapshotOnLoadFailure(t *testing.T) {
	repo := &resolverSettingsRepo{settings: ports.UISettings{PanelPath: "/mypanel", SubPath: "feed"}}
	resolver := NewResolver(repo)
	if got := resolver.PanelPath(); got != "/mypanel" {
		t.Fatalf("PanelPath = %q", got)
	}
	if !resolver.IsSubscription("/feed/token") {
		t.Fatal("initial subscription path was not loaded")
	}

	repo.err = errors.New("database unavailable")
	resolver.Invalidate()
	if got := resolver.PanelPath(); got != "/mypanel" {
		t.Fatalf("PanelPath after load failure = %q; want last good value", got)
	}
	if !resolver.IsSubscription("/feed/token") {
		t.Fatal("subscription path was reset after load failure")
	}
}
