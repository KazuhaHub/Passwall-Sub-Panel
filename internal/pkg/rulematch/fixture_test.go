package rulematch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fixtureProbe struct {
	Host    string `json:"host"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Network string `json:"network"`
	Process string `json:"process"`
	SrcIP   string `json:"src_ip"`
	SrcPort int    `json:"src_port"`
}

type fixtureCase struct {
	Rule   string `json:"rule"`
	Probe  string `json:"probe"`
	Expect string `json:"expect"`
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

// The same file is read by the SPA matcher's test
// (web-react/src/utils/ruleset/rulematch.fixture.test.ts on the UI branch).
func TestRulematchFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web-react", "src", "utils", "ruleset", "rulematch.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Probes map[string]fixtureProbe `json:"probes"`
		Cases  []fixtureCase           `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) < 50 {
		t.Fatalf("fixture looks truncated: %d cases", len(doc.Cases))
	}
	for _, tc := range doc.Cases {
		in, ok := doc.Probes[tc.Probe]
		if !ok {
			t.Fatalf("case %q names unknown probe %q", tc.Rule, tc.Probe)
		}
		probe, err := NewProbe(ProbeInput(in))
		if err != nil {
			t.Fatalf("probe %q: %v", tc.Probe, err)
		}
		got := Eval(ParseRule(tc.Rule), probe)
		if got.Verdict.String() != tc.Expect || (tc.Expect == "unknown" && string(got.Reason) != tc.Reason) {
			t.Errorf("%s on %s: got %s/%s, want %s/%s (%s)", tc.Rule, tc.Probe, got.Verdict, got.Reason, tc.Expect, tc.Reason, tc.Note)
		}
	}
}
