package paneltz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeSettings implements just enough of ports.SettingsRepo to drive
// Location resolution: a configurable cfg and an optional load error.
type fakeSettings struct {
	cfg ports.UISettings
	err error
}

func (f *fakeSettings) Load(_ context.Context, _ ports.UISettings) (ports.UISettings, error) {
	if f.err != nil {
		return ports.UISettings{}, f.err
	}
	return f.cfg, nil
}

func (f *fakeSettings) Save(_ context.Context, _ ports.UISettings) error {
	return nil
}

// TestLocation_LoadsConfigured covers the happy path: a valid IANA name
// in settings returns the corresponding *time.Location.
func TestLocation_LoadsConfigured(t *testing.T) {
	for _, tz := range []string{"Asia/Shanghai", "America/Los_Angeles", "UTC"} {
		t.Run(tz, func(t *testing.T) {
			loc := Location(context.Background(), &fakeSettings{cfg: ports.UISettings{Timezone: tz}})
			if loc.String() != tz {
				t.Errorf("Location for %q = %q, want %q", tz, loc.String(), tz)
			}
		})
	}
}

// TestLocation_FallbacksToLocal documents every degraded path: nil repo,
// load error, blank tz, unparseable IANA name — all should land on
// time.Local so the panel keeps working pre-config.
func TestLocation_FallbacksToLocal(t *testing.T) {
	cases := []struct {
		name string
		repo ports.SettingsRepo
	}{
		{"nil repo", nil},
		{"load error", &fakeSettings{err: errors.New("db down")}},
		{"empty tz", &fakeSettings{cfg: ports.UISettings{Timezone: ""}}},
		{"whitespace tz", &fakeSettings{cfg: ports.UISettings{Timezone: "   "}}},
		{"unparseable iana", &fakeSettings{cfg: ports.UISettings{Timezone: "Not/A/Real/Zone"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := Location(context.Background(), tc.repo)
			if loc != time.Local {
				t.Errorf("Location = %v, want time.Local (fallback)", loc)
			}
		})
	}
}

// TestNow_RespectsLocation: the timestamp Now returns must carry the
// configured tz's Location, so downstream startOfDay / AddDate math
// happens in panel-tz calendar terms rather than server-local terms.
func TestNow_RespectsLocation(t *testing.T) {
	loc := Now(context.Background(), &fakeSettings{cfg: ports.UISettings{Timezone: "Asia/Tokyo"}})
	if loc.Location().String() != "Asia/Tokyo" {
		t.Errorf("Now().Location() = %q, want Asia/Tokyo", loc.Location())
	}
}

// TestValidate_Accepts: every IANA name the binary's tzdata can resolve
// (plus empty, which we explicitly allow as "use server local") must
// pass validation. Without this guarantee, save-time validation would
// reject perfectly valid picks.
func TestValidate_Accepts(t *testing.T) {
	for _, tz := range []string{"", "  ", "UTC", "Asia/Shanghai", "America/Los_Angeles", "Europe/London"} {
		t.Run(tz, func(t *testing.T) {
			if err := Validate(tz); err != nil {
				t.Errorf("Validate(%q) returned error: %v", tz, err)
			}
		})
	}
}

// TestValidate_Rejects: typos and bogus IANA names must be flagged so
// the admin settings PUT handler can return a clear 400 instead of
// silently accepting and falling back to time.Local at use time.
func TestValidate_Rejects(t *testing.T) {
	for _, tz := range []string{"Not/A/Real/Zone", "Asia/Shanghaii", "GMT+8"} {
		t.Run(tz, func(t *testing.T) {
			if err := Validate(tz); err == nil {
				t.Errorf("Validate(%q) accepted a bogus tz", tz)
			}
		})
	}
}
