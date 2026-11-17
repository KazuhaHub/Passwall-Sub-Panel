package ports

import (
	"strings"
	"testing"
)

func TestTransitProbeEffectiveDefaults(t *testing.T) {
	got := TransitProbeEffectiveOf(UISettings{})
	want := TransitProbeEffective{Enabled: true, IntervalSeconds: 60, TimeoutMS: 5000,
		URL: "https://www.gstatic.com/generate_204", Withhold: false, WithholdAfter: 3, RestoreAfter: 3}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	// An empty URL is transport-only, not "not configured".
	if got := TransitProbeEffectiveOf(UISettings{TransitProbeURLOff: true}); got.URL != "" {
		t.Fatalf("an explicitly empty URL runs as %q, want transport-only", got.URL)
	}
	set := UISettings{TransitProbeOff: true, TransitProbeIntervalSeconds: 45, TransitProbeTimeoutMS: 800,
		TransitProbeURL: "http://probe.example:8080/ok", TransitProbeWithhold: true, TransitProbeWithholdAfter: 5, TransitProbeRestoreAfter: 2}
	got = TransitProbeEffectiveOf(set)
	want = TransitProbeEffective{Enabled: false, IntervalSeconds: 45, TimeoutMS: 800, URL: "http://probe.example:8080/ok",
		Withhold: true, WithholdAfter: 5, RestoreAfter: 2}
	if got != want {
		t.Fatalf("effective = %+v, want %+v (settings apply literally)", got, want)
	}
	// A value that could not have been saved (edited into the database) runs as
	// the default rather than something the protocol would refuse to carry.
	got = TransitProbeEffectiveOf(UISettings{TransitProbeIntervalSeconds: 5, TransitProbeURL: "ftp://x"})
	if got.IntervalSeconds != 60 || got.URL != TransitProbeURLDefault {
		t.Fatalf("out-of-range stored values = %+v", got)
	}
}

func TestValidateTransitProbeSettings(t *testing.T) {
	ok := TransitProbeSettings{TransitProbeEnabled: true}
	for _, tc := range []struct {
		name   string
		change func(*TransitProbeSettings)
		field  string
	}{
		{"defaults", func(*TransitProbeSettings) {}, ""},
		{"interval floor", func(s *TransitProbeSettings) { s.TransitProbeIntervalSeconds = 30 }, ""},
		{"interval ceiling", func(s *TransitProbeSettings) { s.TransitProbeIntervalSeconds = 3600 }, ""},
		{"interval too small", func(s *TransitProbeSettings) { s.TransitProbeIntervalSeconds = 29 }, "transit_probe_interval_seconds"},
		{"interval too large", func(s *TransitProbeSettings) { s.TransitProbeIntervalSeconds = 3601 }, "transit_probe_interval_seconds"},
		{"negative interval", func(s *TransitProbeSettings) { s.TransitProbeIntervalSeconds = -1 }, "transit_probe_interval_seconds"},
		{"timeout floor", func(s *TransitProbeSettings) { s.TransitProbeTimeoutMS = 500 }, ""},
		{"timeout too small", func(s *TransitProbeSettings) { s.TransitProbeTimeoutMS = 499 }, "transit_probe_timeout_ms"},
		{"timeout too large", func(s *TransitProbeSettings) { s.TransitProbeTimeoutMS = 10001 }, "transit_probe_timeout_ms"},
		{"withhold after 20", func(s *TransitProbeSettings) { s.TransitProbeWithholdAfter = 20 }, ""},
		{"withhold after 21", func(s *TransitProbeSettings) { s.TransitProbeWithholdAfter = 21 }, "transit_probe_withhold_after"},
		{"restore after negative", func(s *TransitProbeSettings) { s.TransitProbeRestoreAfter = -2 }, "transit_probe_restore_after"},
		{"https url", func(s *TransitProbeSettings) { s.TransitProbeURL = "https://example.com/generate_204" }, ""},
		{"url with port", func(s *TransitProbeSettings) { s.TransitProbeURL = "http://example.com:65535/x" }, ""},
		{"ipv6 url with port", func(s *TransitProbeSettings) { s.TransitProbeURL = "http://[2001:db8::1]:8080/x" }, ""},
		{"port zero", func(s *TransitProbeSettings) { s.TransitProbeURL = "http://example.com:0/x" }, "transit_probe_url"},
		{"port too large", func(s *TransitProbeSettings) { s.TransitProbeURL = "https://example.com:70000/" }, "transit_probe_url"},
		{"empty port", func(s *TransitProbeSettings) { s.TransitProbeURL = "http://example.com:/x" }, "transit_probe_url"},
		{"ftp scheme", func(s *TransitProbeSettings) { s.TransitProbeURL = "ftp://example.com/" }, "transit_probe_url"},
		{"no scheme", func(s *TransitProbeSettings) { s.TransitProbeURL = "example.com/x" }, "transit_probe_url"},
		{"no host", func(s *TransitProbeSettings) { s.TransitProbeURL = "http:///x" }, "transit_probe_url"},
		{"credentials", func(s *TransitProbeSettings) { s.TransitProbeURL = "http://user:pw@example.com/" }, "transit_probe_url"},
		{"space", func(s *TransitProbeSettings) { s.TransitProbeURL = "http://example.com/a b" }, "transit_probe_url"},
		{"too long", func(s *TransitProbeSettings) { s.TransitProbeURL = "http://example.com/" + strings.Repeat("a", 600) }, "transit_probe_url"},
	} {
		s := ok
		tc.change(&s)
		field, err := ValidateTransitProbeSettings(s)
		if field != tc.field || (err != nil) != (tc.field != "") {
			t.Errorf("%s: field %q err %v, want field %q", tc.name, field, err, tc.field)
		}
	}
}

// The settings view carries the real URL (the default filled in), and an empty
// one saves as transport-only.
func TestTransitProbeURLViewAndEmpty(t *testing.T) {
	if got := (UISettings{}).TransitProbeSettings().TransitProbeURL; got != TransitProbeURLDefault {
		t.Fatalf("a panel that never set a URL shows %q", got)
	}
	var s UISettings
	s.SetTransitProbeSettings(TransitProbeSettings{TransitProbeEnabled: true, TransitProbeURL: ""})
	if got := s.TransitProbeSettings().TransitProbeURL; got != "" || TransitProbeEffectiveOf(s).URL != "" {
		t.Fatalf("an empty URL did not stay empty: %q", got)
	}
	s.SetTransitProbeSettings(TransitProbeSettings{TransitProbeEnabled: true, TransitProbeURL: "http://x.example/"})
	if TransitProbeEffectiveOf(s).URL != "http://x.example/" || s.TransitProbeURLOff {
		t.Fatalf("a URL did not clear transport-only: %+v", s)
	}
}

// Probing is stored inverted, so a database that never saw the keys probes,
// and the copy to and from the record keeps every key.
func TestTransitProbeSettingsRoundTrip(t *testing.T) {
	if !(UISettings{}).TransitProbeSettings().TransitProbeEnabled {
		t.Fatal("a record that never saw the keys must probe")
	}
	in := TransitProbeSettings{TransitProbeEnabled: false, TransitProbeIntervalSeconds: 31, TransitProbeTimeoutMS: 501,
		TransitProbeURL: "http://x.example/", TransitProbeWithhold: true, TransitProbeWithholdAfter: 4, TransitProbeRestoreAfter: 5}
	var s UISettings
	s.SetTransitProbeSettings(in)
	if got := s.TransitProbeSettings(); got != in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
}
