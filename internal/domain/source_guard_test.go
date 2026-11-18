package domain

import (
	"errors"
	"fmt"
	"reflect"
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
	key := nodeprotocol.SourceSetKey("src_" + SourceSetSuffix(9223372036854775807, 1<<30))
	if err := key.Validate(); err != nil {
		t.Fatal(err)
	}
	if SourceSetSuffix(3, 1000) == SourceSetSuffix(3, 2000) || SourceSetSuffix(3, 1000) == SourceSetSuffix(4, 1000) {
		t.Fatal("suffix must depend on node and factor")
	}
	if SourceSetSuffix(3, 1000) != SourceSetSuffix(3, 1000) {
		t.Fatal("suffix must be deterministic")
	}
	id, factor, ok := ParseSourceSetKey(string(SourceSetKeyOf(42, 2500)))
	if !ok || id != 42 || factor != 2500 {
		t.Fatalf("round trip = %d %d %v", id, factor, ok)
	}
	for _, bad := range []string{"src_x_f1", "src_7", "src_7_f", "src_7_f-1", "src_07_f1", "cli_3", "src_7_abcdef0123"} {
		if _, _, ok := ParseSourceSetKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestNormalizeSourceCIDRsRefusesTooBroadPrefixes(t *testing.T) {
	for _, bad := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/7", "128.0.0.0/1", "2000::/18", "198.18.0.1/24"} {
		if _, err := NormalizeSourceCIDRs([]string{bad}); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", bad, err)
		}
	}
	for _, ok := range []string{"10.0.0.0/8", "2000::/19", "2001:db8::1/128"} {
		if _, err := NormalizeSourceCIDRs([]string{ok}); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	// A stored prefix that is too broad is not a source address either.
	line := RelayLine{SourceCIDRs: []string{"0.0.0.0/0", "192.0.2.0/24"}}
	if got := line.EffectiveSourceCIDRs(SourceInputs{}); !reflect.DeepEqual(got, []string{"192.0.2.0/24"}) {
		t.Fatalf("effective = %v", got)
	}
}

func TestEffectiveSourceCIDRsIsNeverTruncated(t *testing.T) {
	many := make([]string, 0, MaxSourceCIDRs)
	for i := 0; i < MaxSourceCIDRs; i++ {
		many = append(many, fmt.Sprintf("10.%d.%d.0/24", i/256, i%256))
	}
	line := RelayLine{EntryPanelID: 7, SourceCIDRs: many}
	got := line.EffectiveSourceCIDRs(SourceInputs{EntryAddresses: map[int64][]string{7: {"93.184.216.34"}}})
	if len(got) != MaxSourceCIDRs+1 {
		t.Fatalf("got %d entries, want %d: nothing may be dropped", len(got), MaxSourceCIDRs+1)
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
