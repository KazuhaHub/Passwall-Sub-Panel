package sqlstore

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The group-override editor tells an admin what a stored float override will
// do — apply, or be skipped so the global value stays — and it decides that
// with a TypeScript port of strconv.ParseFloat,
// web-react/src/utils/goParseFloat.ts. The port is tested against a vectors
// file; this test holds the file to the server. It runs every vector through
// the real path a group value takes — applyScopeOverrides, floatField's
// Unmarshal — rather than through ParseFloat directly, so a decoder that one
// day validated, clamped or rejected a value would fail here instead of
// leaving the editor describing a decoder that no longer exists.
//
// If this fails, regenerate the vectors from the decoder and make the port
// agree with them; do not edit an expectation to match the port.
func TestScopeFloatOverrideDecodesLikeTheEditorVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web-react", "src", "utils", "goParseFloat.vectors.json"))
	if err != nil {
		t.Fatalf("read the editor's vectors: %v", err)
	}
	var file struct {
		Groups []struct {
			Name  string `json:"name"`
			Cases []struct {
				In  string `json:"in"`
				Out any    `json:"out"`
			} `json:"cases"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse the editor's vectors: %v", err)
	}

	// The global ratio the override lands on. Not a value any vector
	// decodes to, so "skipped" and "applied" cannot be mistaken for each
	// other.
	const global = 4.25
	n := 0
	for _, g := range file.Groups {
		for _, c := range g.Cases {
			n++
			got := applyScopeOverrides(ports.UISettings{RiskUsageRatio: global},
				[]ports.ScopeOverride{{Type: "risk", Name: "usage_ratio", Value: c.In}}).RiskUsageRatio

			var want float64
			switch out := c.Out.(type) {
			case nil:
				// ParseFloat returned an error: the override is skipped.
				want = global
			case float64:
				want = out
			case string:
				// JSON carries no infinity or NaN, and a -0 does not survive
				// every reader, so the file spells them.
				switch out {
				case "+Inf":
					want = math.Inf(1)
				case "-Inf":
					want = math.Inf(-1)
				case "NaN":
					want = math.NaN()
				case "-0":
					want = math.Copysign(0, -1)
				default:
					t.Fatalf("%s %q: unknown spelled output %q", g.Name, c.In, out)
				}
			default:
				t.Fatalf("%s %q: output %v is neither a number, a spelling nor null", g.Name, c.In, c.Out)
			}
			// Bits, not ==: NaN never equals itself, and -0 == 0.
			same := math.Float64bits(got) == math.Float64bits(want) || (math.IsNaN(got) && math.IsNaN(want))
			if !same {
				t.Errorf("%s %q: the group ratio decodes to %v, the vectors say %v", g.Name, c.In, got, want)
			}
		}
	}
	if n == 0 {
		t.Fatal("the vectors file has no cases: the editor's port is being tested against nothing")
	}
}
