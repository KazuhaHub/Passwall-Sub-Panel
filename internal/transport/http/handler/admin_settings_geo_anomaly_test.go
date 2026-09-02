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
// A UISettings field reaches the admin form through THREE hand-written
// mappings — the DTO struct, the GET direction and the PUT direction — and
// missing any one of them fails silently in the worst possible way: the form
// renders the control, the admin sets it, the save returns 200, and the value
// is discarded. That is the same "writes succeed and do nothing" shape this
// whole area keeps producing, one layer up.
//
// Scoped to geo_anomaly_* rather than every setting because retrofitting the
// rule to the existing surface would fail on fields that are deliberately
// absent (encrypted-at-rest tokens are write-only, ACME fields moved to their
// own page). A narrow guard that holds beats a broad one that gets muted.
func TestSettingsDTOCarriesEveryGeoAnomalyField(t *testing.T) {
	want := jsonTagsWithPrefix(reflect.TypeOf(ports.UISettings{}), "geo_anomaly_")
	if len(want) == 0 {
		t.Fatal("no geo_anomaly_* fields found in UISettings — the guard is pointed at nothing")
	}
	got := jsonTagsWithPrefix(reflect.TypeOf(settingsDTO{}), "geo_anomaly_")
	for tag := range want {
		if !got[tag] {
			t.Errorf("UISettings has %q but the admin settings DTO does not: the form cannot read or write it", tag)
		}
	}
}

// The DTO alone is not enough — a field can be declared and then never
// assigned in either direction. Both mappings are inline in the handler, so
// this reads the source and requires each Go field name to appear on both
// sides of an assignment.
//
// Source inspection rather than a round trip because the mappings live inside
// gin handlers that need a repo, a router and an authenticated context; a
// harness for that would be several hundred lines and would still only prove
// what these two lines prove. Deleting either assignment is the regression
// this exists to catch, and it does catch it.
func TestSettingsHandlerMapsEveryGeoAnomalyFieldBothWays(t *testing.T) {
	src := readHandlerSource(t, "admin_settings.go")
	for _, name := range goFieldNamesWithJSONPrefix(reflect.TypeOf(ports.UISettings{}), "geo_anomaly_") {
		// GET: dto{... Field: s.Field ...}
		if !strings.Contains(src, name+":                s."+name) &&
			!strings.Contains(src, name+":             s."+name) &&
			!strings.Contains(src, "s."+name+",") {
			t.Errorf("%s is never read out of UISettings — the form would always show the zero value", name)
		}
		// PUT: UISettings{... Field: req.Field ...}
		if !strings.Contains(src, "req."+name) {
			t.Errorf("%s is never read off the request — the admin's change would be silently discarded", name)
		}
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
