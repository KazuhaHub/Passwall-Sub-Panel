package xui

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func decodeClientBody(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal client body: %v", err)
	}
	return got
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestBuildClientJSON_CreateShapeIsUnchanged pins the exact key SET the create
// paths emit. This is the whole 3X-UI 3.4.2 .. 3.6.0 regression guarantee: no
// panel older than 3.7.0 is reachable to test against, so the contract has to be
// held by assertion here. A new key leaking into the create shape also inflates
// every unchunked /clients/bulkCreate body against the panel's 10 MiB cap, which
// is the second reason this set is pinned rather than merely spot-checked.
func TestBuildClientJSON_CreateShapeIsUnchanged(t *testing.T) {
	raw, err := buildClientJSON(ports.ClientSpec{
		Email: "u@example.test", Enable: true, ID: "11111111-2222-3333-4444-555555555555",
	})
	if err != nil {
		t.Fatalf("buildClientJSON: %v", err)
	}
	want := []string{"email", "enable", "expiryTime", "id", "limitIp", "reset", "subId", "tgId", "totalGB"}
	if got := sortedKeys(decodeClientBody(t, raw)); !equalStrings(got, want) {
		t.Fatalf("create body key set changed\n got: %v\nwant: %v", got, want)
	}
}

// TestBuildClientUpdateJSON_PinsPanelRenewalOff is the actual regression guard
// for the 3.7.0 defect: PSP must state "no panel-side renewal" explicitly rather
// than relying on the panel's normalizer to turn omitted keys into those values.
// See panelRenewalOff for why the accidental path is not good enough.
func TestBuildClientUpdateJSON_PinsPanelRenewalOff(t *testing.T) {
	raw, err := buildClientUpdateJSON(ports.ClientSpec{
		Email: "u@example.test", Enable: true, ID: "11111111-2222-3333-4444-555555555555",
	})
	if err != nil {
		t.Fatalf("buildClientUpdateJSON: %v", err)
	}
	got := decodeClientBody(t, raw)

	for key, want := range map[string]any{
		"resetDay":        float64(0),
		"resetMax":        float64(0),
		"trafficReset":    "never",
		"trafficResetDay": float64(1),
	} {
		if got[key] != want {
			t.Errorf("update body %q = %#v, want %#v", key, got[key], want)
		}
	}

	// limitHwid must NOT be sent. Echoing a value PSP read moments earlier arms
	// trimClientHwidsForSubID and permanently deletes device registrations on a
	// stale read; omitting writes 0, which leaves that trim dormant. Asserting
	// absence keeps a well-meaning "preserve it too" change from landing without
	// reading why it is unsafe.
	if _, present := got["limitHwid"]; present {
		t.Error("update body must not carry limitHwid — see buildClientUpdateJSON's comment")
	}
}

// TestBuildClientUpdateJSON_IsCreateShapePlusRenewalKeys keeps the two shapes
// from drifting apart in any way other than the four pinned keys.
func TestBuildClientUpdateJSON_IsCreateShapePlusRenewalKeys(t *testing.T) {
	spec := ports.ClientSpec{
		Email: "u@example.test", Enable: true, ID: "11111111-2222-3333-4444-555555555555",
		Flow: "xtls-rprx-vision", Password: "pw", Method: "aes-128-gcm", Auth: "hy2", SubID: "sub", Reset: 3,
	}
	createRaw, err := buildClientJSON(spec)
	if err != nil {
		t.Fatalf("buildClientJSON: %v", err)
	}
	updateRaw, err := buildClientUpdateJSON(spec)
	if err != nil {
		t.Fatalf("buildClientUpdateJSON: %v", err)
	}
	create, update := decodeClientBody(t, createRaw), decodeClientBody(t, updateRaw)

	for k, v := range create {
		if update[k] != v {
			t.Errorf("update body diverges from create on %q: %#v vs %#v", k, update[k], v)
		}
	}
	if len(update) != len(create)+len(panelRenewalOff) {
		t.Errorf("update body has %d keys, want create(%d)+renewal(%d)", len(update), len(create), len(panelRenewalOff))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSpecToRaw_PinsInboundPanelFields is the regression guard for the
// inbound-level clobber. Upstream's UpdateInbound is a full-struct Save, so a
// key PSP omits is written as Go's zero — these four are pinned so that intent
// is explicit rather than a side effect of that binding. See specToRaw.
func TestSpecToRaw_PinsInboundPanelFields(t *testing.T) {
	body := specToRaw(&ports.InboundSpec{
		Remark: "n1", Enable: true, Port: 443, Protocol: "vless",
	}, 7)

	for key, want := range map[string]any{
		"total":           0,
		"trafficReset":    "never",
		"trafficResetDay": 1,
		"disableFlow":     false,
	} {
		if body[key] != want {
			t.Errorf("specToRaw[%q] = %#v, want %#v", key, body[key], want)
		}
	}

	// subSortIndex is preserved, not pinned: UpdateInbound echoes the operator's
	// live value. It must never be baked into the shared body, or every inbound
	// PSP creates or updates would assert a rank it does not own.
	if _, present := body["subSortIndex"]; present {
		t.Error("specToRaw must not carry subSortIndex — UpdateInbound echoes the live value instead")
	}
}

// TestSpecToRaw_KeySetIsPinned catches a field silently joining or leaving the
// inbound body. No pre-3.7.0 panel is reachable in CI, so the request shape has
// to be held by assertion.
func TestSpecToRaw_KeySetIsPinned(t *testing.T) {
	body := specToRaw(&ports.InboundSpec{}, 1)
	want := []string{
		"allocate", "disableFlow", "enable", "expiryTime", "id", "listen",
		"port", "protocol", "remark", "settings", "sniffing", "streamSettings",
		"total", "trafficReset", "trafficResetDay",
	}
	got := make([]string, 0, len(body))
	for k := range body {
		got = append(got, k)
	}
	sort.Strings(got)
	if !equalStrings(got, want) {
		t.Fatalf("inbound body key set changed\n got: %v\nwant: %v", got, want)
	}
}

// TestBuildClientJSON_OperatorLabelsOnlyWhenKnown pins the asymmetry that makes the
// comment carry safe to add incrementally: a caller that did not read the
// client omits the key and keeps the pre-existing blanking, while one that did
// hands the operator's note back. Sending "" instead of omitting would be the
// same blanking with extra steps, so absence is the contract, not a detail.
func TestBuildClientJSON_OperatorLabelsOnlyWhenKnown(t *testing.T) {
	base := ports.ClientSpec{Email: "u@example.test", Enable: true}

	for _, shape := range []struct {
		name string
		fn   func(ports.ClientSpec) (json.RawMessage, error)
	}{
		{"create", buildClientJSON},
		{"update", buildClientUpdateJSON},
	} {
		raw, err := shape.fn(base)
		if err != nil {
			t.Fatalf("%s unknown-comment: %v", shape.name, err)
		}
		unknown := decodeClientBody(t, raw)
		for _, key := range []string{"comment", "group"} {
			if _, present := unknown[key]; present {
				t.Errorf("%s: %q key must be absent when the caller does not know it", shape.name, key)
			}
		}

		withNote := base
		withNote.Comment = "vip — paid through march"
		withNote.Group = "gold"
		raw, err = shape.fn(withNote)
		if err != nil {
			t.Fatalf("%s known-comment: %v", shape.name, err)
		}
		body := decodeClientBody(t, raw)
		if got := body["comment"]; got != "vip — paid through march" {
			t.Errorf("%s: comment = %#v, want the operator's note", shape.name, got)
		}
		if got := body["group"]; got != "gold" {
			t.Errorf("%s: group = %#v, want the operator's label", shape.name, got)
		}
	}
}
