package domain

import (
	"errors"
	"testing"
)

func TestDestinationGroupAccessModeRequiresCanonicalModeAndStage(t *testing.T) {
	for _, tc := range []struct {
		mode, stage string
		want        DestGroupAccessMode
	}{
		{"open", "", DestGroupAccessOpen},
		{"allowlist", "trial", DestGroupAccessTrial},
		{"allowlist", "enforce", DestGroupAccessEnforce},
		{"open", "trial", ""}, {"allowlist", "", ""}, {"allowlist", "unknown", ""}, {"", "", ""},
	} {
		got, err := DestinationGroupAccessMode(tc.mode, tc.stage)
		if got != tc.want || (tc.want == "" && !errors.Is(err, ErrValidation)) || (tc.want != "" && err != nil) {
			t.Errorf("%q/%q got %q: %v", tc.mode, tc.stage, got, err)
		}
	}
}
