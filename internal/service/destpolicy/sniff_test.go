package destpolicy

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-protocol/protocol/conformance"
)

func TestDestinationSniffingUsesSharedConformance(t *testing.T) {
	for _, v := range conformance.SniffingVectors {
		t.Run(v.Name, func(t *testing.T) {
			got := CheckSniffing(v.Sniffing)
			if got.Sufficient != v.Sufficient || got.Inject != v.Inject || got.Reason != v.Reason {
				t.Fatalf("sniffing=%+v want=%+v", got, v)
			}
		})
	}
}
func sniffListener(t *testing.T, id int64, enabled bool, sniff string) protocol.Listener {
	t.Helper()
	body, err := json.Marshal(map[string]any{"enabled": enabled, "sniffing": sniff})
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Listener{Key: protocol.NewListenerKey(id), Config: protocol.RawConfig(body)}
}
func TestPreflightChecksEnabledListenersForDomainsOrProtocolsOnly(t *testing.T) {
	insufficient := `{"enabled":true,"metadataOnly":true,"destOverride":["http","tls","quic"]}`
	listeners := []protocol.Listener{sniffListener(t, 9, true, insufficient), sniffListener(t, 2, true, `{"enabled":true,"destOverride":["http","tls"]}`), sniffListener(t, 3, false, insufficient), sniffListener(t, 4, true, `[]`), sniffListener(t, 5, true, "")}
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:example.com"}}}}
	for _, engine := range []string{"", "xray"} {
		if got := PreflightPolicy(p, engine, listeners); !slices.Equal(got, []protocol.ListenerKey{"lst_2", "lst_9"}) {
			t.Fatalf("%s: insufficient=%v", engine, got)
		}
	}
	if got := PreflightPolicy(p, "sing-box", listeners); len(got) != 0 {
		t.Fatalf("sing-box preflight=%v", got)
	}
	p.Rules[0].Domains = nil
	p.Rules[0].Ports = "443"
	if got := PreflightPolicy(p, "xray", listeners); len(got) != 0 {
		t.Fatalf("port-only policy preflight=%v", got)
	}
	p.Rules[0].Protocols = []string{"bittorrent"}
	if got := PreflightPolicy(p, "xray", listeners); len(got) != 2 {
		t.Fatalf("protocol detection not checked: %v", got)
	}
	if got := PreflightPolicy(nil, "xray", listeners); len(got) != 0 {
		t.Fatal("nil policy preflight")
	}
}
func TestPreflightDoesNotAttributeMalformedListenerToPolicy(t *testing.T) {
	p := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Domains: []string{"domain:example.com"}}}}
	for _, raw := range []string{`[]`, `{"enabled":true,"sniffing":7}`, `{"enabled":true,"sniffing":"["}`, `{"enabled":true,"sniffing":"{\"enabled\":7}"}`} {
		if got := PreflightPolicy(p, "xray", []protocol.Listener{{Key: "lst_1", Config: protocol.RawConfig(raw)}}); len(got) != 0 {
			t.Fatalf("listener error misattributed to policy: %s / %v", raw, got)
		}
	}
}
