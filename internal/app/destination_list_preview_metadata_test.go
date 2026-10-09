package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBuildDestinationListPreviewReportsFullCanonicalIdentity(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	var source, canonical strings.Builder
	source.WriteString("# comments stay in the editor\n*.Example.COM.\n")
	canonical.WriteString("domain:example.com\n")
	for i := range 270 {
		fmt.Fprintf(&source, "domain:s%03d.example.test\n", i)
		fmt.Fprintf(&canonical, "domain:s%03d.example.test\n", i)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	wantDigest := hex.EncodeToString(sum[:])
	input := map[string]any{"name": "Canonical report", "kind": "custom", "text": source.String()}
	w := destinationListRequest(t, a, token, "POST", "lists/preview", input)
	var preview struct {
		ContentSHA256 string   `json:"content_sha256"`
		Entries       []string `json:"entries"`
		EntryCount    int      `json:"entry_count"`
		ParseReport   struct {
			Samples []struct {
				Line       int    `json:"line"`
				Reason     string `json:"reason"`
				Normalized string `json:"normalized"`
			} `json:"samples"`
		} `json:"parse_report"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); w.Code != 200 || err != nil {
		t.Fatal("preview fixture")
	}
	if preview.ContentSHA256 != wantDigest || preview.EntryCount != 271 || len(preview.Entries) != 50 {
		t.Fatal("preview must expose full canonical identity independently of bounded entry samples")
	}
	found := false
	for _, sample := range preview.ParseReport.Samples {
		if sample.Line == 2 && sample.Reason == "normalized" && sample.Normalized == "domain:example.com" {
			found = true
		}
	}
	if !found {
		t.Fatal("normalized report lost the actual replacement value")
	}
	w = destinationListRequest(t, a, token, "POST", "lists", input)
	var saved struct {
		ID            int64          `json:"id"`
		ContentSHA256 string         `json:"content_sha256"`
		EntryTypes    map[string]int `json:"entry_types"`
		Entries       []string       `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); w.Code != 201 || err != nil || saved.ContentSHA256 != wantDigest {
		t.Fatal("saved detail lost canonical identity")
	}
	w = destinationListRequest(t, a, token, "GET", fmt.Sprintf("lists/%d", saved.ID), nil)
	if err := json.Unmarshal(w.Body.Bytes(), &saved); w.Code != 200 || err != nil || saved.ContentSHA256 != wantDigest {
		t.Fatal("read detail lost canonical identity")
	}
	if saved.EntryTypes["domain"] != 271 || len(saved.Entries) != 200 {
		t.Fatal("type totals must count the complete canonical list, not the first 200 entry samples")
	}
}
