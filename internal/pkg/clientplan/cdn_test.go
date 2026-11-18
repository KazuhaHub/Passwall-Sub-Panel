package clientplan

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A CDN line is a line of its landing like any external line: one client per
// user on the landing's own listener, with a derived identity of its own and
// the line's factor (one-for-one unless set). A disabled line has none.
func TestACDNLineHasItsOwnClientOnTheLandingsListener(t *testing.T) {
	milli := 2500
	landing := &domain.Node{ID: 1, PanelID: 7, DesiredProtocol: "vless", Relays: []domain.RelayLine{
		{ID: "cdn", Enabled: true, Mode: domain.RelayModeCDN, Domain: "cdn.example.com"},
		{ID: "cdn2", Enabled: true, Mode: domain.RelayModeCDN, Domain: "b.example.com", TrafficFactorMilli: &milli},
		{ID: "off", Enabled: false, Mode: domain.RelayModeCDN, Domain: "c.example.com"},
	}}
	lines := LineCredsFromNodes([]*domain.Node{landing})
	if len(lines) != 3 {
		t.Fatalf("lines = %+v, want the direct line and two CDN lines", lines)
	}
	byID := map[string]LineCred{}
	for _, l := range lines {
		byID[l.Line.LineID] = l
	}
	for id, want := range map[string]int{"cdn": 1000, "cdn2": 2500} {
		got, ok := byID[id]
		if !ok || got.ListenerNodeID != 1 || got.FactorMilli == nil || *got.FactorMilli != want {
			t.Errorf("%s = %+v, want listener 1 and factor %d", id, got, want)
		}
	}
	if _, ok := byID["off"]; ok {
		t.Error("a disabled CDN line was planned")
	}
	if len(TunnelLineCreds([]*domain.Node{landing})) != 0 {
		t.Error("a CDN line has no listener of its own")
	}

	user := "11111111-2222-4333-8444-555555555555"
	built := Build(5, user, 7, domain.EmailRules{Domain: "x.test"}, lines, nil, nil)
	seen := map[string]bool{}
	for _, row := range built {
		if row.Client.LineID == "" || row.Client.LineID == domain.DirectLineID {
			if row.Client.UUID != user {
				t.Errorf("the direct line keeps the user's identity, got %s", row.Client.UUID)
			}
			continue
		}
		if row.Client.UUID == user || seen[row.Client.UUID] {
			t.Errorf("line %s reused an identity: %s", row.Client.LineID, row.Client.UUID)
		}
		seen[row.Client.UUID] = true
		if want := domain.DeriveLineUUID(user, domain.LineKey{NodeID: 1, LineID: row.Client.LineID}, 1); row.Client.UUID != want {
			t.Errorf("line %s uuid = %s, want %s", row.Client.LineID, row.Client.UUID, want)
		}
	}
	if len(seen) != 2 {
		t.Errorf("built %d line identities, want 2", len(seen))
	}
}

// Traffic through a CDN line is booked at the line's own factor, and a delta
// that arrives after the line is deleted keeps its last price.
func TestCDNLineFactorBooking(t *testing.T) {
	milli := 3000
	landing := &domain.Node{ID: 1, Relays: []domain.RelayLine{
		{ID: "cdn", Enabled: true, Mode: domain.RelayModeCDN, TrafficFactorMilli: &milli},
		{ID: "dflt", Enabled: true, Mode: domain.RelayModeCDN},
	}}
	factors := domain.LineFactors([]*domain.Node{landing})
	if got := factors[domain.LineKey{NodeID: 1, LineID: "cdn"}]; got != 3000 {
		t.Fatalf("factor = %d", got)
	}
	if got := factors[domain.LineKey{NodeID: 1, LineID: "dflt"}]; got != 1000 {
		t.Fatalf("default factor = %d, want 1000", got)
	}
	if got := domain.ChargeBytes(1000, factors[domain.LineKey{NodeID: 1, LineID: "cdn"}]); got != 3000 {
		t.Fatalf("charged = %d", got)
	}
}
