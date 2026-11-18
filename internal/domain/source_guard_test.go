package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func TestNormalizeSourceCIDRs(t *testing.T) {
	got, err := NormalizeSourceCIDRs([]string{" 203.0.113.9 ", "10.0.0.0/8", "2001:db8::1", "10.0.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "10.0.0.0/24", "203.0.113.9/32", "2001:db8::1/128"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for name, in := range map[string][]string{
		"host bits":   {"10.0.0.1/8"},
		"mapped":      {"::ffff:10.0.0.1"},
		"mapped cidr": {"::ffff:10.0.0.0/104"},
		"garbage":     {"nope"},
		"duplicate":   {"10.0.0.1", "10.0.0.1/32"},
		"zone":        {"fe80::1%eth0"},
	} {
		if _, err := NormalizeSourceCIDRs(in); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	many := make([]string, MaxSourceCIDRs+1)
	for i := range many {
		many[i] = "10." + string(rune('0'+i/256%10)) + "." + "0.0/16"
	}
	if _, err := NormalizeSourceCIDRs(many); !errors.Is(err, ErrValidation) {
		t.Errorf("too many: err = %v", err)
	}
	if got, err := NormalizeSourceCIDRs(nil); err != nil || got != nil {
		t.Errorf("nil: %v %v", got, err)
	}
}

func TestEffectiveSourceCIDRs(t *testing.T) {
	in := SourceInputs{
		EntryAddresses: map[int64][]string{7: {"198.51.100.4", "2001:db8::4"}},
		Cloudflare:     []string{"173.245.48.0/20"},
	}
	cases := []struct {
		name string
		line RelayLine
		want []string
	}{
		{"managed", RelayLine{EntryPanelID: 7, SourceCIDRs: []string{"192.0.2.0/24"}},
			[]string{"192.0.2.0/24", "198.51.100.4/32", "2001:db8::4/128"}},
		{"managed unknown entry", RelayLine{EntryPanelID: 8}, nil},
		{"external", RelayLine{SourceCIDRs: []string{"192.0.2.1/32"}}, []string{"192.0.2.1/32"}},
		{"external empty", RelayLine{}, nil},
		{"cdn default", RelayLine{Mode: RelayModeCDN}, []string{"173.245.48.0/20"}},
		{"cdn own", RelayLine{Mode: RelayModeCDN, SourceCIDRs: []string{"192.0.2.0/24"}}, []string{"192.0.2.0/24"}},
		{"tunnel", RelayLine{Mode: RelayModeTunnel, EntryPanelID: 7}, nil},
	}
	for _, c := range cases {
		if got := c.line.EffectiveSourceCIDRs(in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestSourceSetSuffixIsAValidKey(t *testing.T) {
	key := nodeprotocol.SourceSetKey("src_" + SourceSetSuffix(9223372036854775807, strings.Repeat("x", 200)))
	if err := key.Validate(); err != nil {
		t.Fatal(err)
	}
	if SourceSetSuffix(3, "a") == SourceSetSuffix(3, "b") || SourceSetSuffix(3, "a") == SourceSetSuffix(4, "a") {
		t.Fatal("suffix must depend on node and line")
	}
	if SourceSetSuffix(3, "a") != SourceSetSuffix(3, "a") {
		t.Fatal("suffix must be deterministic")
	}
}

func TestSourceGuardWithheld(t *testing.T) {
	two := 2000
	n := &Node{}
	cheap := RelayLine{Enabled: true}
	dear := RelayLine{Enabled: true, TrafficFactorMilli: &two}
	managedDear := RelayLine{Enabled: true, EntryPanelID: 5, TrafficFactorMilli: &two}
	managed := RelayLine{Enabled: true, EntryPanelID: 5}
	cdnDear := RelayLine{Enabled: true, Mode: RelayModeCDN, TrafficFactorMilli: &two}
	tunnelDear := RelayLine{Mode: RelayModeTunnel, EntryPanelID: 5, TrafficFactorMilli: &two}
	for _, c := range []struct {
		name               string
		line               RelayLine
		canGuard, entryKnn bool
		want               string
	}{
		{"cannot guard same factor", cheap, false, false, ""},
		{"cannot guard differing", dear, false, false, SourceWithheldFactor},
		{"cannot guard differing cdn", cdnDear, false, false, SourceWithheldFactor},
		{"tunnel exempt", tunnelDear, false, false, ""},
		{"can guard external without addresses, same factor", cheap, true, false, ""},
		{"can guard external without addresses, differing", dear, true, false, SourceWithheldUnknown},
		{"can guard external with addresses", RelayLine{SourceCIDRs: []string{"192.0.2.1/32"}, TrafficFactorMilli: &two}, true, false, ""},
		{"can guard cdn differing", cdnDear, true, false, ""},
		{"managed unknown", managed, true, false, SourceWithheldUnknown},
		{"managed known", managedDear, true, true, ""},
		{"managed unknown but own addresses", RelayLine{EntryPanelID: 5, SourceCIDRs: []string{"192.0.2.1/32"}}, true, false, ""},
	} {
		if got := n.SourceGuardWithheld(c.line, c.canGuard, c.entryKnn); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	n.Relays = []RelayLine{cheap, dear}
	if line, bad := n.SourceGuardFactorProblem(); !bad || line.EffectiveTrafficFactor() != 2000 {
		t.Fatalf("factor problem not found: %v %v", line, bad)
	}
	n.Relays = []RelayLine{cheap, tunnelDear}
	if _, bad := n.SourceGuardFactorProblem(); bad {
		t.Fatal("a tunnel line does not count")
	}
}

func TestNodeAgentSourceAddresses(t *testing.T) {
	agent := &NodeAgent{
		InterfaceAddresses: []string{"198.51.100.7", "10.0.0.5", "2001:db8::1", "203.0.113.1"},
		ObservedAddress:    "::ffff:93.184.216.34",
	}
	// 198.51.100.0/24, 2001:db8::/32 and 203.0.113.0/24 are documentation
	// ranges: a landing never sees them.
	if got := agent.SourceAddresses(); !reflect.DeepEqual(got, []string{"93.184.216.34"}) {
		t.Fatalf("got %v", got)
	}
	agent = &NodeAgent{InterfaceAddresses: []string{"93.184.216.35", "93.184.216.34"}, ObservedAddress: "93.184.216.34"}
	if got := agent.SourceAddresses(); !reflect.DeepEqual(got, []string{"93.184.216.34", "93.184.216.35"}) {
		t.Fatalf("got %v", got)
	}
	if (&NodeAgent{ObservedAddress: "not an address"}).SourceAddresses() != nil {
		t.Fatal("garbage became an address")
	}
	if (&NodeAgent{}).CanSourceGuard() || !(&NodeAgent{ObservedCapabilities: []string{nodeprotocol.CapabilitySourceGuard}}).CanSourceGuard() {
		t.Fatal("capability misread")
	}
}
