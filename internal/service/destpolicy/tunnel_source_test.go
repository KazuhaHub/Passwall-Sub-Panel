package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// A source rule set on a landing node applies to the listeners of the landing's
// tunnel lines as well: the line belongs to the landing, and a setting applies
// to the item it is set on. On the landing's own server it covers the landing
// alone; on the entry server, only the tunnel listener.
func TestASourceRuleOnALandingCoversItsTunnelListeners(t *testing.T) {
	b := &base{sources: []baseSource{{key: "k", cidrs: []string{"203.0.113.0/24"}, nodes: []int64{10}}}}
	sourceInbounds := func(v nodeView) []protocol.ListenerKey {
		policy := b.assemble(v)
		if policy == nil {
			return nil
		}
		for _, r := range policy.Rules {
			if len(r.SourceCIDRs) > 0 {
				return r.Inbounds
			}
		}
		return nil
	}
	landingServer := sourceInbounds(nodeView{panelID: 1, nodeIDs: map[int64]bool{10: true}})
	if len(landingServer) != 1 || landingServer[0] != protocol.NewListenerKey(10) {
		t.Fatalf("on the landing's server = %v", landingServer)
	}
	entryServer := sourceInbounds(nodeView{panelID: 2, nodeIDs: map[int64]bool{20: true, 21: true}, parents: map[int64]int64{20: 10}})
	if len(entryServer) != 1 || entryServer[0] != protocol.NewListenerKey(20) {
		t.Fatalf("on the entry server = %v, want only the listener of the landing's tunnel line", entryServer)
	}
	if none := sourceInbounds(nodeView{panelID: 2, nodeIDs: map[int64]bool{21: true}}); none != nil {
		t.Fatalf("an unrelated listener got the rule: %v", none)
	}
}
