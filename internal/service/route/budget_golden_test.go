package route

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

type goldenCase struct {
	Name    string                      `json:"name"`
	Engine  string                      `json:"engine"`
	Policy  protocol.DestinationPolicy  `json:"policy"`
	Clients map[protocol.SubjectKey]int `json:"clients"`
	Present []protocol.ListenerKey      `json:"present"`
	Groups  []struct {
		Listeners []protocol.ListenerKey `json:"listeners"`
		Default   protocol.LinkKey       `json:"default"`
		Rules     []protocol.RouteRule   `json:"rules"`
	} `json:"groups"`
	Expect struct {
		Entries int `json:"entries"`
		Rules   int `json:"rules"`
	} `json:"expect"`
}

// The budget the panel computes at save time is the one the node counts. These
// fixtures were run through the node's own compilers (see the source note in the
// file) and the numbers it produced are what PSP must give for the same policy,
// roster and routes. A change to either side that moves a number fails here.
func TestBudgetEqualsTheNodesCountOnTheNodesOwnFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/routebudget.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Source string       `json:"source"`
		Cases  []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.Source == "" || len(file.Cases) == 0 {
		t.Fatal("the fixtures say nothing of where their numbers come from")
	}
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			present := map[protocol.ListenerKey]bool{}
			for _, key := range c.Present {
				present[key] = true
			}
			var groups []routeGroup
			for _, g := range c.Groups {
				groups = append(groups, routeGroup{Listeners: g.Listeners, Default: g.Default, Rules: g.Rules})
			}
			policy := c.Policy
			got := analyse(analysisInput{Policy: &policy, Resolve: policy.Resolve, Engine: c.Engine, Clients: c.Clients, Present: present, Groups: groups})
			if got.Over != nil {
				t.Fatalf("over a limit: %+v", got.Over)
			}
			if got.Entries != c.Expect.Entries || got.Rules != c.Expect.Rules {
				t.Fatalf("entries %d rules %d, the node counts %d and %d", got.Entries, got.Rules, c.Expect.Entries, c.Expect.Rules)
			}
		})
	}
}
