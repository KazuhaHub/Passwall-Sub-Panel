package render

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// lineCredentialRepo holds several client rows, one per line (ADR 0043).
type lineCredentialRepo struct {
	ports.PSPClientRepo
	clients     []*domain.PSPClient
	attachments map[int64][]domain.PSPClientInbound
}

func (r lineCredentialRepo) ListByUser(context.Context, int64) ([]*domain.PSPClient, error) {
	return r.clients, nil
}

func (r lineCredentialRepo) ListInbounds(_ context.Context, id int64) ([]domain.PSPClientInbound, error) {
	return r.attachments[id], nil
}

func renderLines(t *testing.T, relayApplied bool) []map[string]any {
	t.Helper()
	node := vlessRealityNode(true)
	node.Relays = []domain.RelayLine{{ID: "r1", Name: "relay", Address: "relay.example", Port: 8443, Enabled: true}}
	relayAttachment := domain.PSPClientInbound{ClientID: 2, NodeID: node.ID, State: domain.ClientApplyPending}
	if relayApplied {
		relayAttachment.AppliedUUID, relayAttachment.AppliedEmail = "relay-line-uuid", "u5-lr@x"
	}
	repo := lineCredentialRepo{
		clients: []*domain.PSPClient{
			{ID: 1, UserID: 5, NodeID: node.ID, LineID: domain.DirectLineID, UUID: "uuid-of-user-5"},
			{ID: 2, UserID: 5, NodeID: node.ID, LineID: "r1", UUID: "relay-line-uuid"},
		},
		attachments: map[int64][]domain.PSPClientInbound{
			1: {{ClientID: 1, NodeID: node.ID, State: domain.ClientApplyApplied, AppliedUUID: "uuid-of-user-5", AppliedEmail: "u5@x"}},
			2: {relayAttachment},
		},
	}
	s := &Service{
		repos: ports.Repos{
			Settings:  fakeSettings{ports.UISettings{EmailDomain: "x"}},
			PSPClient: repo,
			XUIPanel:  renderPanelRepo{kind: domain.PanelKindPSP},
		},
		pool: panicPool{},
	}
	items := expandRelays([]renderItem{{name: "US-1", node: node}})
	return s.buildProxies(context.Background(), &domain.User{ID: 5, UUID: "uuid-of-user-5"}, items, ports.UISettings{EmailDomain: "x"})
}

// Each line renders the identity of its own client: the direct line the
// user's UUID, the relay line its derived one.
func TestRenderEachLineWithItsOwnAppliedIdentity(t *testing.T) {
	out := renderLines(t, true)
	if len(out) != 2 || out[0]["uuid"] != "uuid-of-user-5" || out[1]["uuid"] != "relay-line-uuid" || out[1]["server"] != "relay.example" {
		t.Fatalf("proxies = %#v", out)
	}
}

// Until its own identity is applied, a relay line is not published: the
// landing refuses the direct identity from a relay's source (ADR 0044), so
// borrowing it would hand out a line that cannot connect.
func TestRenderRelayLineWaitsForItsOwnIdentity(t *testing.T) {
	out := renderLines(t, false)
	if len(out) != 1 || out[0]["uuid"] != "uuid-of-user-5" || out[0]["server"] == "relay.example" {
		t.Fatalf("proxies = %#v", out)
	}
}

// Right after the upgrade a converted row has its line (the lowest node's
// direct line) but is still attached to every node it served: until its
// resync narrows it, it is the direct line of each, so no node drops out of
// the subscription. A legacy row with no line at all behaves the same.
func TestRenderConvertedRowServesEveryDirectLineUntilNarrowed(t *testing.T) {
	a, b := vlessRealityNode(true), vlessRealityNode(true)
	b.ID = a.ID + 1
	for _, line := range []string{domain.DirectLineID, ""} {
		nodeID := a.ID
		if line == "" {
			nodeID = 0
		}
		assertConvertedRowServesBoth(t, a, b, &domain.PSPClient{ID: 1, UserID: 5, NodeID: nodeID, LineID: line, UUID: "uuid-of-user-5"})
	}
}

func assertConvertedRowServesBoth(t *testing.T, a, b *domain.Node, row *domain.PSPClient) {
	t.Helper()
	repo := lineCredentialRepo{
		clients: []*domain.PSPClient{row},
		attachments: map[int64][]domain.PSPClientInbound{1: {
			{ClientID: 1, NodeID: a.ID, AppliedUUID: "uuid-of-user-5"},
			{ClientID: 1, NodeID: b.ID, AppliedUUID: "uuid-of-user-5"},
		}},
	}
	applied := (&Service{repos: ports.Repos{PSPClient: repo}}).appliedClientCredentialsByNode(context.Background(), &domain.User{ID: 5})
	if applied[domain.DirectLine(a.ID)].UUID != "uuid-of-user-5" || applied[domain.DirectLine(b.ID)].UUID != "uuid-of-user-5" {
		t.Fatalf("applied = %#v", applied)
	}
}
