package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The concurrent-location ignore list is the one geo setting the form
// validates, because a typo in it fails OPEN: an entry that does not parse
// matches nothing, the relay it was meant to cover keeps reading as a place,
// and nothing downstream can repair that toward not accusing (a nonsense
// tolerance, by contrast, is repaired by domain.GeoPolicyFromSettings). So a
// save carrying a bad entry must be refused before anything is written, and
// the refusal must say WHICH entry, or an admin with fifty lines has to guess.
func TestSettingsPut_RejectsBadIgnoreEntryNamingIt(t *testing.T) {
	repo := &nodeTaskLifecycleSettingsRepo{settings: ports.UISettings{LoginMode: "local_only"}}
	router := nodeTaskLifecycleSettingsRouter(repo)

	payload, _ := json.Marshal(map[string]any{
		"login_mode":                   "local_only",
		"geo_anomaly_ignore_addresses": "203.0.113.7 # office\n1.2.3.999, 198.51.100.0/24\n10.0.0.0/33",
	})
	rec := requestNodeTaskLifecycleSettings(t, router, http.MethodPut, string(payload))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT with a bad ignore entry = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	for _, bad := range []string{"1.2.3.999", "10.0.0.0/33"} {
		if !strings.Contains(body.Error, bad) {
			t.Errorf("the error must name every bad entry; %q is missing from %q", bad, body.Error)
		}
	}
	if strings.Contains(body.Error, "203.0.113.7") || strings.Contains(body.Error, "198.51.100.0/24") {
		t.Errorf("valid entries must not be reported as bad: %q", body.Error)
	}
	if repo.saves != 0 {
		t.Fatalf("a refused save must write nothing: saves=%d", repo.saves)
	}
}

// Addresses and CIDRs, comma- or newline-separated with comments, are what
// the hint tells an admin to type; all of it must be accepted and kept as
// typed (only the outer whitespace trimmed) so the comments survive the
// round trip and the form shows the admin their own list back.
func TestSettingsPut_AcceptsIPsAndCIDRs(t *testing.T) {
	repo := &nodeTaskLifecycleSettingsRepo{settings: ports.UISettings{LoginMode: "local_only"}}
	router := nodeTaskLifecycleSettingsRouter(repo)

	list := "203.0.113.7 # office exit\n198.51.100.0/24, 2001:db8::/32\n::ffff:192.0.2.1"
	payload, _ := json.Marshal(map[string]any{
		"login_mode":                   "local_only",
		"geo_anomaly_ignore_addresses": "\n  " + list + "  \n",
	})
	rec := requestNodeTaskLifecycleSettings(t, router, http.MethodPut, string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT with a valid ignore list = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if repo.saves != 1 {
		t.Fatalf("saves = %d, want 1", repo.saves)
	}
	if got := repo.settings.GeoAnomalyIgnoreAddresses; got != list {
		t.Fatalf("stored ignore list = %q, want it trimmed and otherwise as typed: %q", got, list)
	}

	// And the GET the form loads from must hand it back, or the next save
	// from a form that never saw it would write an empty list over it.
	rec = requestNodeTaskLifecycleSettings(t, router, http.MethodGet, "")
	var got struct {
		IgnoreAddresses string `json:"geo_anomaly_ignore_addresses"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if got.IgnoreAddresses != list {
		t.Fatalf("GET geo_anomaly_ignore_addresses = %q, want %q", got.IgnoreAddresses, list)
	}
}
