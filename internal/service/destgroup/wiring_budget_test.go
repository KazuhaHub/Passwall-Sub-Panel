package destgroup

import "testing"

type perPanelBudget bool

func (p perPanelBudget) GroupBudgetPerPanel() bool { return bool(p) }

// The wiring report reads the budget's own answer; a budget that cannot say
// is not counted as per panel.
func TestBudgetPerPanelReadsTheBudgetsAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		budget any
		want   bool
	}{
		"per panel":        {perPanelBudget(true), true},
		"summing":          {perPanelBudget(false), false},
		"cannot say":       {struct{}{}, false},
		"no budget at all": {nil, false},
	} {
		if got := budgetPerPanel(tc.budget); got != tc.want {
			t.Errorf("%s: budgetPerPanel = %v, want %v", name, got, tc.want)
		}
	}
}
