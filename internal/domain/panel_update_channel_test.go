package domain

import "testing"

func TestPanelUpdateChannelIsExplicitAndDefaultsSafely(t *testing.T) {
	for _, test := range []struct {
		raw       PanelUpdateChannel
		valid     bool
		effective PanelUpdateChannel
	}{
		{PanelUpdateStable, true, PanelUpdateStable}, {PanelUpdateBeta, true, PanelUpdateBeta},
		{"", false, PanelUpdateStable}, {"testing", false, PanelUpdateStable},
		{"BETA", false, PanelUpdateStable}, {"future-custom", false, PanelUpdateStable},
	} {
		if test.raw.Valid() != test.valid || test.raw.Effective() != test.effective {
			t.Errorf("channel %q: valid=%v effective=%q", test.raw, test.raw.Valid(), test.raw.Effective())
		}
	}
}
