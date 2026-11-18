package sqlstore

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A CDN line is part of the landing's own row: it round-trips with every field
// (the domain, the optimised address, port, SNI, Host and the factor), is
// loaded as published with no entry state at all, and goes with the node.
func TestACDNLineRoundTripsAndIsPublishedWithItsLanding(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	factor := 2500
	landing := &domain.Node{
		PanelID: 7, InboundID: 1, DisplayName: "JP", ServerAddress: "land.example.com",
		DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true,
		// A landing that cannot guard by source publishes only lines that cost the
		// same as its direct line (ADR 0044).
		TrafficFactorMilli: &factor,
		Relays: []domain.RelayLine{
			{ID: "fwd", Name: "gz", Address: "gz.relay.cn", Port: 20001, Enabled: true, Tags: []string{}},
			{ID: "cdn-1", Name: "CF", Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Address: "104.16.1.1", Port: 8443,
				SNI: "cdn.example.com", Host: "host.example.com", Enabled: true, Region: "JP", Tags: []string{"cdn"}, TrafficFactorMilli: &factor},
		},
	}
	if err := nodes.Create(ctx, landing); err != nil {
		t.Fatal(err)
	}
	got, err := nodes.GetByID(ctx, landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	fwd, cdn := got.Relays[0], got.Relays[1]
	if fwd.CDN() || fwd.Domain != "" || fwd.Mode != "" {
		t.Fatalf("the forward line changed: %+v", fwd)
	}
	if !cdn.CDN() || cdn.Domain != "cdn.example.com" || cdn.Address != "104.16.1.1" || cdn.Port != 8443 || cdn.SNI != "cdn.example.com" ||
		cdn.Host != "host.example.com" || cdn.EntryPanelID != 0 || cdn.EffectiveTrafficFactor() != 2500 || len(cdn.Tags) != 1 {
		t.Fatalf("stored CDN line = %+v", cdn)
	}
	if cdn.Managed() || cdn.Gated() || !cdn.Publishable() || cdn.ManagedReady || cdn.Probe != nil {
		t.Fatalf("a CDN line has no entry state and is published with its landing: %+v", cdn)
	}

	// Editing the line in place keeps it a CDN line; deleting the node removes it.
	got.Relays[1].Address = "cdn.example.com"
	if err := nodes.UpdateMetadata(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := nodes.GetByID(ctx, landing.ID)
	if c := again.Relays[1]; !c.CDN() || c.Address != "cdn.example.com" || c.Domain != "cdn.example.com" {
		t.Fatalf("edited line = %+v", c)
	}
	if err := nodes.Delete(ctx, landing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := nodes.GetByID(ctx, landing.ID); err == nil {
		t.Fatal("the node and its CDN line should be gone")
	}
}
