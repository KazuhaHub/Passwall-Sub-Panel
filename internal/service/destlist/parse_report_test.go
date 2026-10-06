package destlist

import (
	"encoding/json"
	"testing"
)

func TestNormalizedSamplesExposeActualCanonicalReplacement(t *testing.T) {
	parsed, err := ParseCustom([]byte("*.Example.COM.\nregexp:.*\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(parsed.Report)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Samples []struct {
			Reason     string `json:"reason"`
			Normalized string `json:"normalized"`
		} `json:"samples"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Samples) != 2 || report.Samples[0].Reason != "normalized" || report.Samples[0].Normalized != "domain:example.com" || report.Samples[1].Reason != "broad_entry" || report.Samples[1].Normalized != "" {
		t.Fatal("replacement metadata must describe normalization, not a rejected broad entry")
	}
}
