package xui

import (
	"encoding/json"
	"testing"
)

func TestFlexJSON_UnmarshalNestedObject(t *testing.T) {
	// 3X-UI 3.1.0 settings form: a nested JSON object.
	in := []byte(`{"settings": {"clients":[{"email":"a"}],"decryption":"none"}}`)
	var got struct {
		Settings flexJSON `json:"settings"`
	}
	if err := json.Unmarshal(in, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Downstream code re-parses the stored bytes — confirm round-trip works.
	var inner struct {
		Clients []struct {
			Email string `json:"email"`
		} `json:"clients"`
		Decryption string `json:"decryption"`
	}
	if err := json.Unmarshal([]byte(got.Settings), &inner); err != nil {
		t.Fatalf("nested form round-trip: %v (raw: %s)", err, string(got.Settings))
	}
	if inner.Decryption != "none" || len(inner.Clients) != 1 || inner.Clients[0].Email != "a" {
		t.Fatalf("nested form decoded unexpectedly: %#v", inner)
	}
}

func TestFlexJSON_UnmarshalNestedArray(t *testing.T) {
	in := []byte(`{"x": [1,2,3]}`)
	var got struct {
		X flexJSON `json:"x"`
	}
	if err := json.Unmarshal(in, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got.X) != `[1,2,3]` {
		t.Fatalf("nested-array form: got %q", string(got.X))
	}
}

func TestFlexJSON_UnmarshalNull(t *testing.T) {
	// 3.1.0 returns `allocate: null` on inbounds with no allocate block.
	// Must normalise to empty string so downstream `if s == ""` checks fire.
	in := []byte(`{"settings": null}`)
	var got struct {
		Settings flexJSON `json:"settings"`
	}
	if err := json.Unmarshal(in, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got.Settings) != "" {
		t.Fatalf("null form: got %q, want empty", string(got.Settings))
	}
}

func TestFlexJSON_UnmarshalMissingField(t *testing.T) {
	in := []byte(`{}`)
	var got struct {
		Settings flexJSON `json:"settings"`
	}
	if err := json.Unmarshal(in, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got.Settings) != "" {
		t.Fatalf("absent field: got %q, want empty", string(got.Settings))
	}
}
