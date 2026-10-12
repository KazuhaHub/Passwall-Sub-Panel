package app

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const nativeFleetXUIImage = "ghcr.io/mhsanaei/3x-ui:v3.9.0@sha256:93a7a68e3d0a65be1b02d2bfc1f3ca02d0e800516a403e103cea2d7c7090855d"
const nativeFleetSUIImage = "ghcr.io/alireza0/s-ui:v1.6.3@sha256:51cdb0036118dbcac12cba28fed13e36c53f8cdcb72bff88a062b5d6f243bd64"

func TestNativeFleetStableReceiptRequiresFreshHeartbeatAndExactCandidate(t *testing.T) {
	minted, requested, heartbeat := time.Unix(100, 0), time.Unix(101, 0), time.Unix(102, 0)
	base := domain.DestAgentPolicy{AgentID: "fleet", MintedKind: domain.DestCandidateEmpty, MintedGeneration: 7, MintedAt: &minted, MintedContext: "owned", MintedBody: []byte("null"), ReportedState: "applied", ReportedAt: &minted}
	// The observer preserves ReportedAt for identical reports. A newer real
	// heartbeat plus the exact unchanged candidate must still prove readiness.
	if !nativeMutationReceiptMatches(&base, base, 7, &heartbeat, requested) {
		t.Fatal("stable fleet receipt was mistaken for a missing current heartbeat")
	}
	if nativeMutationReceiptMatches(&base, base, 7, &minted, requested) {
		t.Fatal("old fleet heartbeat confirmed current readiness")
	}
	// A first candidate can be minted before the first wait while the prior
	// transaction still exposes an unminted row. Startup's clock is authority.
	unminted := domain.DestAgentPolicy{AgentID: base.AgentID}
	startup := minted.Add(-time.Second)
	if !nativeMutationReceiptMatches(&unminted, base, 7, &heartbeat, startup) || nativeMutationReceiptMatches(&unminted, base, 7, &heartbeat, requested) {
		t.Fatal("initial fleet receipt lost the actual daemon startup boundary")
	}
	for _, change := range []func(*domain.DestAgentPolicy){
		func(r *domain.DestAgentPolicy) { r.AgentID = "borrowed" },
		func(r *domain.DestAgentPolicy) { r.MintedBody = []byte("different") },
		func(r *domain.DestAgentPolicy) { r.MintedGeneration = 6 },
		func(r *domain.DestAgentPolicy) { r.ReportedSHA256 = "borrowed" },
		func(r *domain.DestAgentPolicy) { r.ReportedState = "rejected" },
	} {
		current := base
		change(&current)
		if nativeMutationReceiptMatches(&base, current, 7, &heartbeat, requested) {
			t.Fatal("changed or borrowed fleet receipt was accepted")
		}
	}
}

func nativeFleetProfile(profile string) ([2]bool, bool) {
	switch profile {
	case "old":
		return [2]bool{false, false}, true
	case "new":
		return [2]bool{true, true}, true
	case "mixed":
		return [2]bool{false, true}, true
	default:
		return [2]bool{}, false
	}
}

type nativeFleetNodePlan struct {
	capable    bool
	engine     domain.NodeCoreEngine
	version    string
	restricted bool
}

func nativeFleetNodes(profile, layout string) ([]nativeFleetNodePlan, bool) {
	capabilities, valid := nativeFleetProfile(profile)
	if !valid || layout != "xray-pair" && layout != "mixed-engines" {
		return nil, false
	}
	cores := []nativeFleetNodePlan{{engine: domain.NodeCoreXray, version: "26.6.27"}}
	if layout == "mixed-engines" {
		cores = append(cores,
			nativeFleetNodePlan{engine: domain.NodeCoreXray, version: "26.7.28"},
			nativeFleetNodePlan{engine: domain.NodeCoreXray, version: "26.9.9", restricted: true},
			nativeFleetNodePlan{engine: domain.NodeCoreSingBox, version: "1.14.0"},
		)
	}
	var peers []nativeFleetNodePlan
	for _, core := range cores {
		for _, capable := range capabilities {
			peer := core
			peer.capable = capable
			peers = append(peers, peer)
		}
	}
	return peers, true
}

func TestNativeFleetMixedEnginePlanCoversEveryCandidateAndCore(t *testing.T) {
	for _, profile := range []string{"old", "new", "mixed"} {
		capabilities, _ := nativeFleetProfile(profile)
		for layout, count := range map[string]int{"xray-pair": 2, "mixed-engines": 8} {
			peers, ok := nativeFleetNodes(profile, layout)
			if !ok || len(peers) != count {
				t.Fatal("fleet layout omitted an independent daemon")
			}
			cores := []struct{ engine, version string }{{"xray", "26.6.27"}, {"xray", "26.7.28"}, {"xray", "26.9.9"}, {"sing-box", "1.14.0"}}
			for i, peer := range peers {
				core := cores[i/2]
				if string(peer.engine) != core.engine || peer.version != core.version || peer.capable != capabilities[i%2] || peer.restricted != (core.engine == "xray" && core.version == "26.9.9") {
					t.Fatal("fleet layout changed the candidate, reviewed core or explicit restriction acknowledgement")
				}
			}
		}
	}
	for _, input := range [][2]string{{"", "mixed-engines"}, {"old", ""}, {"mixed", "other"}, {"new", "mixed-engines; private"}} {
		if peers, ok := nativeFleetNodes(input[0], input[1]); ok || len(peers) != 0 {
			t.Fatal("unknown fleet shape silently selected an easier layout")
		}
	}
}

func nativeFleetPolicyRules(body *protocol.DestinationPolicy, groupID, userID int64, port int, controlAddress, stage string) bool {
	if body == nil || len(body.Exempt) != 0 {
		return false
	}
	want := 1
	if stage != "" {
		want = 3
	}
	if len(body.Rules) != want {
		return false
	}
	global, allowance, tail := 0, 0, 0
	for _, rule := range body.Rules {
		if len(rule.Domains) != 0 || len(rule.Protocols) != 0 || rule.Network != "" {
			return false
		}
		switch rule.ID {
		case "p1":
			if rule.Action != protocol.RuleBlock || rule.Ports != strconv.Itoa(port) || len(rule.Subjects) != 0 || len(rule.CIDRs) != 0 || rule.CatchAll {
				return false
			}
			global++
		case fmt.Sprintf("g%dx2", groupID):
			if stage == "" || rule.Action != protocol.RuleAllow || len(rule.Subjects) != 1 || rule.Subjects[0] != protocol.NewSubjectKey(userID) || len(rule.CIDRs) != 1 || rule.CIDRs[0] != controlAddress+"/128" || rule.Ports != "" || rule.CatchAll {
				return false
			}
			allowance++
		case fmt.Sprintf("g%d", groupID):
			action := protocol.RuleObserve
			if stage == "enforce" {
				action = protocol.RuleBlock
			} else if stage != "trial" {
				return false
			}
			if rule.Action != action || !rule.CatchAll || len(rule.Subjects) != 1 || rule.Subjects[0] != protocol.NewSubjectKey(userID) || len(rule.CIDRs) != 0 || rule.Ports != "" {
				return false
			}
			tail++
		default:
			return false
		}
	}
	return global == 1 && (stage == "" && allowance == 0 && tail == 0 || stage != "" && allowance == 1 && tail == 1)
}

func TestNativeFleetPolicyRequiresOwnSubjectAllowanceAndCatchAll(t *testing.T) {
	global := protocol.DestinationRule{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}
	allow := protocol.DestinationRule{ID: "g7x2", Action: protocol.RuleAllow, Subjects: []protocol.SubjectKey{protocol.NewSubjectKey(9)}, CIDRs: []string{"2001:db8::9/128"}}
	tail := protocol.DestinationRule{ID: "g7", Action: protocol.RuleBlock, Subjects: []protocol.SubjectKey{protocol.NewSubjectKey(9)}, CatchAll: true}
	check := func(body *protocol.DestinationPolicy) bool {
		return nativeFleetPolicyRules(body, 7, 9, 443, "2001:db8::9", "enforce")
	}
	if !check(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{global, allow, tail}}) || !nativeFleetPolicyRules(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{global}}, 7, 9, 443, "2001:db8::9", "") {
		t.Fatal("complete owned fleet policy was rejected")
	}
	for _, change := range []func(*protocol.DestinationPolicy){
		func(b *protocol.DestinationPolicy) { b.Rules = b.Rules[:2] },
		func(b *protocol.DestinationPolicy) {
			b.Rules[1].Subjects = []protocol.SubjectKey{protocol.NewSubjectKey(10)}
		},
		func(b *protocol.DestinationPolicy) { b.Rules[1].CIDRs = []string{"2001:db8::10/128"} },
		func(b *protocol.DestinationPolicy) { b.Rules[2].CatchAll = false },
		func(b *protocol.DestinationPolicy) { b.Rules[2].Action = protocol.RuleObserve },
		func(b *protocol.DestinationPolicy) { b.Rules[0].Ports = "444" },
		func(b *protocol.DestinationPolicy) { b.Rules = append(b.Rules, b.Rules[1]) },
	} {
		body := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{global, allow, tail}}
		change(body)
		if check(body) {
			t.Fatal("fleet policy borrowed another subject or omitted required execution")
		}
	}
	if check(nil) {
		t.Fatal("missing fleet policy was accepted")
	}
}

type nativeFleetContainer struct {
	ID         string `json:"Id"`
	Name       string
	Config     struct{ Image string }
	HostConfig struct{ NetworkMode string }
	Mounts     []struct{ Source, Destination string }
	State      struct{ Running bool }
}

func nativeFleetContainerOwned(info nativeFleetContainer, name, image, source, destination string) bool {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(info.ID) || info.Name != "/"+name || info.Config.Image != image || info.HostConfig.NetworkMode != "host" || !filepath.IsAbs(source) {
		return false
	}
	matches := 0
	for _, mount := range info.Mounts {
		if mount.Destination == destination {
			if filepath.Clean(mount.Source) != filepath.Clean(source) {
				return false
			}
			matches++
		}
	}
	return matches == 1
}

func TestNativeFleetProfilesAndContainerOwnershipAreClosed(t *testing.T) {
	for profile, want := range map[string][2]bool{"old": {false, false}, "new": {true, true}, "mixed": {false, true}} {
		if got, ok := nativeFleetProfile(profile); !ok || got != want {
			t.Fatal("fleet profile changed the individual candidate capabilities")
		}
	}
	for _, profile := range []string{"", "baseline", "new; private", "OLD"} {
		if _, ok := nativeFleetProfile(profile); ok {
			t.Fatal("unknown fleet profile silently became an easier cohort")
		}
	}
	source := filepath.Join(t.TempDir(), "3xui")
	var owned nativeFleetContainer
	owned.ID, owned.Name, owned.Config.Image, owned.HostConfig.NetworkMode = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "/psp-compat-3xui", nativeFleetXUIImage, "host"
	owned.Mounts = append(owned.Mounts, struct{ Source, Destination string }{source, "/etc/x-ui"})
	check := func(info nativeFleetContainer) bool {
		return nativeFleetContainerOwned(info, "psp-compat-3xui", nativeFleetXUIImage, source, "/etc/x-ui")
	}
	if !check(owned) {
		t.Fatal("owned fixture cannot be identified for cleanup")
	}
	for _, change := range []func(*nativeFleetContainer){
		func(c *nativeFleetContainer) { c.ID = "foreign" },
		func(c *nativeFleetContainer) { c.Name = "/foreign-container" },
		func(c *nativeFleetContainer) { c.Config.Image = "unreviewed:latest" },
		func(c *nativeFleetContainer) { c.HostConfig.NetworkMode = "bridge" },
		func(c *nativeFleetContainer) { c.Mounts = nil },
		func(c *nativeFleetContainer) { c.Mounts = append(c.Mounts, c.Mounts[0]) },
		func(c *nativeFleetContainer) {
			c.Mounts = []struct{ Source, Destination string }{{filepath.Dir(source), "/etc/x-ui"}}
		},
	} {
		current := owned
		current.Mounts = append([]struct{ Source, Destination string }{}, owned.Mounts...)
		change(&current)
		if check(current) {
			t.Fatal("changed container provenance was admitted for deletion")
		}
	}
}
