package render

import "testing"

func TestProxySentinel_Shape(t *testing.T) {
	s := proxySentinel()
	if s["name"] != "PSP-NoNodes" {
		t.Errorf("sentinel name = %v, want PSP-NoNodes", s["name"])
	}
	if s["type"] != "direct" {
		t.Errorf("sentinel type = %v, want direct (must be a no-op)", s["type"])
	}
}

func TestWithSentinelIfEmpty_PassThroughNonEmpty(t *testing.T) {
	real := []map[string]any{
		{"name": "TW-01", "type": "vless"},
		{"name": "JP-01", "type": "trojan"},
	}
	got := withSentinelIfEmpty(real)
	if len(got) != 2 {
		t.Fatalf("non-empty input must pass through unchanged, got len=%d", len(got))
	}
	if got[0]["name"] != "TW-01" || got[1]["name"] != "JP-01" {
		t.Errorf("got %v, expected ordering preserved", got)
	}
}

func TestWithSentinelIfEmpty_InjectsOnEmpty(t *testing.T) {
	for _, in := range [][]map[string]any{nil, {}} {
		got := withSentinelIfEmpty(in)
		if len(got) != 1 {
			t.Fatalf("empty input must produce 1 sentinel, got %d (input was %v)", len(got), in)
		}
		if got[0]["name"] != "PSP-NoNodes" {
			t.Errorf("sentinel name = %v, want PSP-NoNodes (input was %v)", got[0]["name"], in)
		}
	}
}

func TestWithSentinelIfEmpty_SingleSeparatorStillCountsAsEmpty(t *testing.T) {
	// Edge case: a single separator entry is technically a proxy block
	// from the renderer's perspective (it emits a fake DIRECT with a
	// "name" prefix). withSentinelIfEmpty operates on the FINAL list and
	// doesn't peer into entries — so a separator-only list is treated as
	// non-empty, and the CMfA validator gets a 1-entry proxies array.
	// That's strictly more permissive than rejecting it; document the
	// behaviour rather than special-casing separator detection here.
	sepOnly := []map[string]any{
		{"name": "── Premium ──", "type": "direct"},
	}
	got := withSentinelIfEmpty(sepOnly)
	if len(got) != 1 || got[0]["name"] != "── Premium ──" {
		t.Fatalf("separator-only must pass through, got %v", got)
	}
}
