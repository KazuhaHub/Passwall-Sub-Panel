package handler

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Drift guard for the settings transport.
//
// A UISettings field reaches the admin settings page through two hand-written
// mappings — the DTO struct and the GET direction — and missing either fails
// silently: the page (and the scope editor, whose inheritance baseline reads
// this GET) shows the zero value as if it were stored. The PUT direction is
// deliberately absent for these prefixes: the risk center's policy endpoint
// owns them, and TestSettingsPut_NeverReadsPolicyFieldsFromTheRequest pins
// that this page's save never reads them off the request.
//
// Scoped to named prefixes rather than every setting because retrofitting the
// rule to the existing surface would fail on fields that are deliberately
// absent (encrypted-at-rest tokens are write-only, ACME fields moved to their
// own page). A narrow guard that holds beats a broad one that gets muted.
//
// guardedSettingPrefixes is the list those prefixes come from: the
// concurrent-location policy, and the risk signals that reuse it. A new
// family of detector knobs joins by adding its prefix here, not by copying
// the two tests below.
var guardedSettingPrefixes = []string{"geo_anomaly_", "risk_"}

func TestSettingsDTOCarriesEveryGeoAnomalyField(t *testing.T) {
	for _, prefix := range guardedSettingPrefixes {
		t.Run(prefix, func(t *testing.T) {
			want := jsonTagsWithPrefix(reflect.TypeOf(ports.UISettings{}), prefix)
			if len(want) == 0 {
				t.Fatalf("no %s* fields found in UISettings — the guard is pointed at nothing", prefix)
			}
			got := jsonTagsWithPrefix(reflect.TypeOf(settingsDTO{}), prefix)
			for tag := range want {
				if !got[tag] {
					t.Errorf("UISettings has %q but the admin settings DTO does not: the form cannot read or write it", tag)
				}
			}
		})
	}
}

// The DTO alone is not enough — a field can be declared and never assigned.
// The mapping is inline in settingsToDTO, so this reads the source and
// requires each Go field name to be read out of the stored settings.
//
// Source inspection rather than a round trip because the mapping lives beside
// gin handlers that need a repo, a router and an authenticated context; a
// harness for that would be several hundred lines and would still only prove
// what this check proves. Deleting the assignment is the regression this
// exists to catch, and it does catch it.
func TestSettingsHandlerServesEveryGeoAnomalyField(t *testing.T) {
	src := readHandlerSource(t, "admin_settings.go")
	for _, prefix := range guardedSettingPrefixes {
		t.Run(prefix, func(t *testing.T) {
			names := goFieldNamesWithJSONPrefix(reflect.TypeOf(ports.UISettings{}), prefix)
			if len(names) == 0 {
				t.Fatalf("no %s* fields found in UISettings — the guard is pointed at nothing", prefix)
			}
			for _, name := range names {
				// GET: dto{... Field: s.Field ...}
				if !strings.Contains(src, "s."+name+",") {
					t.Errorf("%s is never read out of UISettings — the page would always show the zero value", name)
				}
			}
		})
	}
}

func jsonTagsWithPrefix(t reflect.Type, prefix string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if strings.HasPrefix(tag, prefix) {
			out[tag] = true
		}
	}
	return out
}

func goFieldNamesWithJSONPrefix(t reflect.Type, prefix string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if strings.HasPrefix(tag, prefix) {
			out = append(out, t.Field(i).Name)
		}
	}
	return out
}

func readHandlerSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
