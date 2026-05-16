package xrayspec

import (
	"encoding/json"
	"testing"
)

// 3X-UI's admin form sometimes stores tgId as a quoted string, sometimes as a
// raw JSON number (admin pastes a Telegram numeric ID). Either form must
// parse — otherwise the inbound becomes invisible to reconcile and the node
// detail page surfaces "parse settings: cannot unmarshal number".
func TestParseSettings_TgIDString(t *testing.T) {
	out, err := ParseSettings(`{"clients":[{"email":"a","tgId":"12345"}]}`)
	if err != nil {
		t.Fatalf("string tgId: %v", err)
	}
	if got := string(out.Clients[0].TgID); got != "12345" {
		t.Fatalf("string tgId = %q, want %q", got, "12345")
	}
}

func TestParseSettings_TgIDNumber(t *testing.T) {
	out, err := ParseSettings(`{"clients":[{"email":"a","tgId":12345}]}`)
	if err != nil {
		t.Fatalf("number tgId: %v", err)
	}
	if got := string(out.Clients[0].TgID); got != "12345" {
		t.Fatalf("number tgId = %q, want %q", got, "12345")
	}
}

func TestParseSettings_TgIDInt64(t *testing.T) {
	// Telegram user IDs comfortably exceed int32. Make sure a 10-digit raw
	// number doesn't lose precision via float64.
	out, err := ParseSettings(`{"clients":[{"email":"a","tgId":7123456789}]}`)
	if err != nil {
		t.Fatalf("int64 tgId: %v", err)
	}
	if got := string(out.Clients[0].TgID); got != "7123456789" {
		t.Fatalf("int64 tgId = %q, want %q", got, "7123456789")
	}
}

func TestParseSettings_TgIDNullOrMissing(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"null", `{"clients":[{"email":"a","tgId":null}]}`},
		{"missing", `{"clients":[{"email":"a"}]}`},
		{"empty_string", `{"clients":[{"email":"a","tgId":""}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := ParseSettings(c.in)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if got := string(out.Clients[0].TgID); got != "" {
				t.Fatalf("%s tgId = %q, want empty", c.name, got)
			}
		})
	}
}

func TestInboundClient_TgIDRoundTrip(t *testing.T) {
	// After unmarshaling a number we should re-marshal as a string so 3X-UI
	// stops oscillating between the two encodings when the panel echoes a
	// client back.
	c := InboundClient{}
	if err := json.Unmarshal([]byte(`{"tgId":12345}`), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `"tgId":"12345"`
	if !contains(string(b), want) {
		t.Fatalf("marshal output = %s, want substring %s", string(b), want)
	}
}

func TestInboundClient_TgIDEmptyOmitted(t *testing.T) {
	// Empty tgId should still honour omitempty so we don't push noise back to
	// 3X-UI on round-trips that never touched the field.
	c := InboundClient{Email: "a"}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if contains(string(b), "tgId") {
		t.Fatalf("empty tgId leaked into output: %s", string(b))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
