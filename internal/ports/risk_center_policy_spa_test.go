package ports

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestRiskCenterPolicyKeysMatchTheSPA holds the policy page's key list
// (web-react/src/views/admin/risk/policy/policyKeys.json) to the policy
// itself: the same 48 tags, in struct order. The page lays out, diffs and
// saves exactly those keys, and it PUTs only the ones an admin changed, so a
// key the server added but the page never learned would be editable nowhere,
// and a key the page kept after the server dropped it would be sent and
// silently ignored. The order is held too, so a diff of the two files reads
// as the change it is.
func TestRiskCenterPolicyKeysMatchTheSPA(t *testing.T) {
	path := filepath.Join("..", "..", "web-react", "src", "views", "admin", "risk", "policy", "policyKeys.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the policy page's key list: %v", err)
	}
	var spa []string
	if err := json.Unmarshal(raw, &spa); err != nil {
		t.Fatalf("%s is not a JSON array of strings: %v", path, err)
	}
	if want := RiskCenterPolicyKeys(); !slices.Equal(spa, want) {
		t.Fatalf("policyKeys.json drifted from ports.RiskCenterPolicy:\n got %q\nwant %q", spa, want)
	}
}
