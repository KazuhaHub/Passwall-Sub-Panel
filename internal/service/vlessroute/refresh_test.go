package vlessroute

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestRefreshDetectsRemoteRuleChangesImmediatelyWithoutWriting(t *testing.T) {
	for _, field := range []string{"ip", "port", "outboundTag"} {
		t.Run(field, func(t *testing.T) {
			s, n, c := serviceFixture(t)
			if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}}); err != nil {
				t.Fatal(err)
			}
			original := c.template
			var root map[string]any
			if err := json.Unmarshal(c.template.Config, &root); err != nil {
				t.Fatal(err)
			}
			rules := root["routing"].(map[string]any)["rules"].([]any)
			rule := rules[len(rules)-1].(map[string]any)
			switch field {
			case "ip":
				rule[field] = []string{"192.0.2.1"}
			case "port":
				rule[field] = "8080"
			case "outboundTag":
				rule[field] = "manually-changed"
			}
			c.template.Config, _ = json.Marshal(root)
			changed := append(json.RawMessage(nil), c.template.Config...)
			view, err := s.Refresh(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if view.State.Status != "drift" || view.State.Error != "routing_or_outbound_changed" || n.node.RouteState.Status != "drift" {
				t.Fatalf("refresh missed %s change: %+v", field, view)
			}
			if c.writes != 1 || !sameConfig(changed, c.template.Config) {
				t.Fatal("refresh overwrote the external template")
			}
			c.template = original
			view, err = s.Refresh(context.Background(), 1)
			if err != nil || view.State.Status != "applied" || c.writes != 1 {
				t.Fatalf("restored rule was not rechecked: %+v %v", view, err)
			}
		})
	}
}

func TestRefreshUnavailableRemoteWithdrawsWithoutWriting(t *testing.T) {
	s, n, c := serviceFixture(t)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	c.readError = true
	view, err := s.Refresh(context.Background(), 1)
	if err != nil || view.State.Status != "unknown" || view.State.Error != "remote_check_unavailable" || n.node.RouteState.Status != "unknown" || c.writes != 1 {
		t.Fatalf("unavailable refresh left stale success: %+v %v", view, err)
	}
}

func TestExplicitRefreshReportsBusyButBackgroundCheckStillSkips(t *testing.T) {
	s, n, c := serviceFixture(t)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	n.leaseBusy = true
	reads := c.reads
	if _, err := s.Refresh(context.Background(), 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("busy refresh returned a stale success: %v", err)
	}
	if err := s.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.reads != reads || c.writes != 1 {
		t.Fatal("busy check touched remote state")
	}
}

func TestRefreshEmptyNodeDoesNotInventAppliedState(t *testing.T) {
	s, _, c := serviceFixture(t)
	view, err := s.Refresh(context.Background(), 1)
	if err != nil || view.State.Status != "" || c.reads != 0 || c.writes != 0 {
		t.Fatalf("empty node acquired a fake applied state: %+v %v", view, err)
	}
}
