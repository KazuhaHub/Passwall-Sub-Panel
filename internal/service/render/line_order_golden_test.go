package render

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestUpgradeWithoutPositionsRendersIdenticalBytes is the compatibility
// guarantee of per-line ordering: with no line position stored anywhere, every
// subscription body in every format is byte-for-byte what the pipeline produced
// before the feature existed. The goldens under testdata/line_order were
// captured from the pre-change code (applyLayout + expandRelays, nothing
// after), so this compares against the old pipeline, not against itself.
//
// Regenerating them (PSP_UPDATE_GOLDEN=1) is only legitimate when the output is
// meant to change for everyone.
func TestUpgradeWithoutPositionsRendersIdenticalBytes(t *testing.T) {
	for _, ct := range allClientTypes {
		t.Run(string(ct), func(t *testing.T) {
			r := awkwardFleet(t)
			out, err := r.RenderForUser(context.Background(), r.user, ct)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "line_order", string(ct)+".golden")
			if os.Getenv("PSP_UPDATE_GOLDEN") != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, out.Body, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(out.Body) {
				t.Fatalf("%s body changed without any line position stored\nwant:\n%s\ngot:\n%s", ct, want, out.Body)
			}
		})
	}
}
