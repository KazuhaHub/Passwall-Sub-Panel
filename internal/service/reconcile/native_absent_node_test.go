package reconcile

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// asyncClient reports itself as a native (asynchronous) panel.
type asyncClient struct{ recClient }

func (*asyncClient) ApplyIsAsynchronous() bool { return true }

// checkNodes runs with a cache that holds node 1's inbound (a sibling that was
// reported) but not node 2's.
func absentNodeFixture(c ports.XUIClient) (*Service, *recNodeRepo, map[inboundCacheKey]*inboundCacheEntry) {
	sibling := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, Enabled: true}
	absent := &domain.Node{ID: 2, PanelID: 1, InboundID: 4, Enabled: true}
	repo := &recNodeRepo{nodes: []*domain.Node{sibling, absent}}
	svc := &Service{nodes: repo, pool: recPool{c: c}}
	cache := cacheFromInbounds(1, []ports.Inbound{{ID: 3, Protocol: "vless", Port: 443, Settings: `{"clients":[]}`}})
	return svc, repo, cache
}

// On a native panel the report lacks any node whose config object is not applied
// yet, or whose apply failed (a port conflict, say). Absence there means "not
// applied", which is surfaced through object status and issues; turning the node
// off for it would also be permanent, because the intent carries Enabled.
func TestCheckNodesNeverDisablesANativeNodeForBeingAbsentFromTheReport(t *testing.T) {
	svc, repo, cache := absentNodeFixture(&asyncClient{})
	report := &Report{}
	svc.checkNodes(context.Background(), report, cache, nil)
	if len(repo.enabledSets) != 0 {
		t.Fatalf("native node disabled for absence: %v", repo.enabledSets)
	}
	for _, is := range report.Issues {
		if is.Code == "inbound_missing_disabled_node" {
			t.Fatalf("unexpected issue %+v", is)
		}
	}
}

// 3X-UI is unchanged: an enabled node whose inbound vanished from a reachable
// panel is disabled and reported.
func TestCheckNodesStillDisablesAXUINodeWhoseInboundVanished(t *testing.T) {
	svc, repo, cache := absentNodeFixture(&recClient{})
	report := &Report{}
	svc.checkNodes(context.Background(), report, cache, nil)
	if len(repo.enabledSets) != 1 || repo.enabledSets[0] != false {
		t.Fatalf("enabledSets = %v, want one disable", repo.enabledSets)
	}
	found := 0
	for _, is := range report.Issues {
		if is.Code == "inbound_missing_disabled_node" && is.InboundID == 4 {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("issues=%+v", report.Issues)
	}
}
