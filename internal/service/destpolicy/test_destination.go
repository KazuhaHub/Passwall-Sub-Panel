package destpolicy

import (
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/net/idna"
)

type DestinationTestInput struct {
	Target          string
	Port            uint16
	Network         string
	UserID, PanelID int64
}
type DestinationTestStep struct {
	Step     string `json:"step"`
	PolicyID int64  `json:"policy_id,omitempty"`
	GroupID  int64  `json:"group_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Result   string `json:"result"`
	ListID   int64  `json:"list_id,omitempty"`
	Entry    string `json:"entry,omitempty"`
}
type DestinationTestNode struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name"`
	State   string `json:"state"`
}
type DestinationTestResult struct {
	Verdict         string                `json:"verdict"`
	TerminatingStep *string               `json:"terminating_step"`
	Steps           []DestinationTestStep `json:"steps"`
	Notes           []string              `json:"notes"`
	Unpublished     bool                  `json:"unpublished"`
	Nodes           []DestinationTestNode `json:"nodes"`
}

// NormalizeTestTarget parses locally. DNS is deliberately absent from both the
// normalizer and evaluator; resolved addresses cannot participate in this test.
func NormalizeTestTarget(raw string) (string, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\t") {
		return "", nil, invalid("target")
	}
	host := raw
	notes := []string{}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
			return "", nil, invalid("target")
		}
		if port := u.Port(); port != "" {
			p, err := strconv.Atoi(port)
			if err != nil || p < 1 || p > 65535 {
				return "", nil, invalid("target")
			}
		}
		host, notes = u.Hostname(), append(notes, "url_host_only")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return "", nil, invalid("target")
		}
		return ip.Unmap().String(), append(notes, "ip_domain_rules_not_matched"), nil
	}
	host, err := idna.Lookup.ToASCII(strings.ToLower(strings.TrimSuffix(host, ".")))
	if err != nil || len(host) > 253 || len(host) == 0 {
		return "", nil, invalid("target")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", nil, invalid("target")
		}
		for _, ch := range label {
			if ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
				return "", nil, invalid("target")
			}
		}
	}
	return host, append(notes, "domain_not_resolved"), nil
}

// EvaluateDestinationTest is pure: it builds the logical policy from the
// selected immutable publication plus the current roster. Actual node state is
// reported separately and neither Compile, publication nor candidate mint runs.
func EvaluateDestinationTest(input DestinationTestInput, current domain.DestTestContext, poll time.Duration, now time.Time) (DestinationTestResult, error) {
	result := DestinationTestResult{Steps: []DestinationTestStep{}, Notes: []string{"destination_only_preview"}, Nodes: []DestinationTestNode{}, Unpublished: current.State.Generation > current.State.PublishedGeneration}
	target, notes, err := NormalizeTestTarget(input.Target)
	if err != nil {
		return DestinationTestResult{}, err
	}
	if input.Port == 0 {
		return DestinationTestResult{}, invalid("port")
	}
	if input.Network != "tcp" && input.Network != "udp" {
		return DestinationTestResult{}, invalid("network")
	}
	if poll <= 0 {
		poll = time.Duration(protocol.DefaultNextPollSeconds) * time.Second
	}
	for _, p := range current.Panels {
		result.Nodes = append(result.Nodes, DestinationTestNode{PanelID: p.ID, Name: p.Name, State: destinationTestNodeState(p, current.State, poll, now)})
	}
	result.Notes = append(result.Notes, notes...)
	defs := domain.DestDefinitions{}
	if current.Published {
		defs, err = DecodeDefinitionSnapshot(current.Snapshot)
		if err != nil {
			return DestinationTestResult{}, err
		}
		if defs.State.PublishedGeneration != current.State.PublishedGeneration {
			return DestinationTestResult{}, domain.ErrUnavailable
		}
	} else if current.State.PublishedGeneration != 0 {
		return DestinationTestResult{}, domain.ErrUnavailable
	}
	// Live emergency pause takes priority even when its publication failed.
	defs.State.Paused = current.State.Paused
	if input.UserID > 0 && input.PanelID == 0 && current.SelectedPanelID == 0 {
		result.Verdict, result.Notes = "untestable", append(result.Notes, "no_native_client")
		return result, nil
	}
	roster := RosterInput{UserIDs: current.UserIDs, UserGroups: current.UserGroups, Collect: domain.AuditCollectOff, Capabilities: []string{protocol.CapabilityDestinationPolicy}}
	if current.SelectedPanelID == 0 {
		// Anonymous virtual evaluation has no group or exemption membership.
		roster.UserIDs, roster.UserGroups = nil, map[int64]int64{}
		result.Notes = append(result.Notes, "virtual_scope")
	}
	if input.UserID > 0 && !slices.Contains(roster.UserIDs, input.UserID) {
		result.Notes = append(result.Notes, "user_not_in_node_roster")
	}
	policy, err := BuildPolicy(defs, roster)
	if err != nil {
		return DestinationTestResult{}, err
	}
	subject := protocol.SubjectKey("")
	if input.UserID > 0 {
		subject = protocol.NewSubjectKey(input.UserID)
	}
	matched := protocol.MatchDestination(policy, subject, target, input.Port, input.Network)
	if matched.Verdict == "invalid" {
		return DestinationTestResult{}, invalid("target")
	}
	result.Verdict = matched.Verdict
	for _, m := range matched.Steps {
		step := DestinationTestStep{Result: m.Result}
		switch {
		case m.RuleID == "psp-exempt":
			step.Step = "exemption"
		case m.RuleID == "":
			step.Step = "direct"
		default:
			idText, _, _ := strings.Cut(m.RuleID[1:], "x")
			id, err := strconv.ParseInt(idText, 10, 64)
			if err != nil || id <= 0 {
				return DestinationTestResult{}, domain.ErrUnavailable
			}
			if strings.HasPrefix(m.RuleID, "g") {
				step.Step, step.GroupID, step.Name = "group", id, current.GroupNames[id]
			} else {
				step.Step, step.PolicyID, step.Name = string(m.Action), id, current.PolicyNames[id]
			}
			step.ListID, step.Entry = testMatchedEntry(policy, m, defs, subject, target, input.Port, input.Network)
		}
		result.Steps = append(result.Steps, step)
		if m.Result == "hit" && result.TerminatingStep == nil {
			name := step.Step
			result.TerminatingStep = &name
		}
	}
	if result.Verdict == "untestable" {
		result.TerminatingStep = nil
		result.Notes = append(result.Notes, "protocol_not_testable")
	}
	if current.State.Paused {
		result.Notes = append(result.Notes, "paused")
	}
	return result, nil
}

func testMatchedEntry(policy *protocol.DestinationPolicy, matched protocol.MatchStep, defs domain.DestDefinitions, subject protocol.SubjectKey, target string, port uint16, network string) (int64, string) {
	if policy == nil || matched.Result != "hit" && matched.Result != "shadowed" {
		return 0, ""
	}
	for _, rule := range policy.Rules {
		if rule.ID != matched.RuleID {
			continue
		}
		for _, entry := range append(append([]string(nil), rule.Domains...), rule.CIDRs...) {
			one := rule
			// The enclosing trace already established subject applicability.
			one.Subjects = nil
			one.Domains, one.CIDRs, one.Protocols, one.Private, one.CatchAll = nil, nil, nil, false, false
			if _, err := netip.ParsePrefix(entry); err == nil {
				one.CIDRs = []string{entry}
			} else {
				one.Domains = []string{entry}
			}
			trace := protocol.MatchDestination(&protocol.DestinationPolicy{Rules: []protocol.DestinationRule{one}}, subject, target, port, network)
			found := false
			for _, s := range trace.Steps {
				found = found || s.RuleID == one.ID && s.Result == "hit"
			}
			if !found {
				continue
			}
			// Only lists referenced by this source can supply provenance.
			var ids []int64
			text, _, _ := strings.Cut(rule.ID[1:], "x")
			id, _ := strconv.ParseInt(text, 10, 64)
			if strings.HasPrefix(rule.ID, "p") {
				for _, p := range defs.Policies {
					if p.ID == id {
						ids = p.ListIDs
						break
					}
				}
			} else {
				for _, g := range defs.Groups {
					if g.GroupID == id {
						if strings.HasSuffix(rule.ID, "x1") {
							ids = g.ListIDs
						} else if strings.HasSuffix(rule.ID, "x2") {
							ids = positiveIDs(g.BaseListID, g.ExtraListID)
						}
						break
					}
				}
			}
			for _, listID := range ids {
				for _, l := range defs.Lists {
					if l.ID == listID && slices.Contains(strings.Split(strings.TrimSpace(string(l.Entries)), "\n"), entry) {
						return listID, entry
					}
				}
			}
			return 0, entry
		}
	}
	return 0, ""
}

func destinationTestNodeState(p domain.DestTestPanel, state domain.DestPolicyState, poll time.Duration, now time.Time) string {
	if p.Kind != domain.PanelKindPSP {
		return "unsupported_kind"
	}
	if p.Agent == nil || !slices.Contains(p.Agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) {
		return "unsupported_version"
	}
	if p.Agent.LastSeen == nil || now.Sub(*p.Agent.LastSeen) > 3*poll {
		return "offline"
	}
	if state.Paused {
		return "paused"
	}
	r := p.Runtime
	if r == nil {
		if state.PublishedGeneration == 0 {
			return "none"
		}
		return "pending"
	}
	if r.OverLimit != nil {
		return "over_limit"
	}
	if len(r.PrecheckListeners) > 0 {
		return "sniffing"
	}
	if r.FallbackReason != "" || r.FallbackExhausted || r.ReportedState == "rejected" {
		return "rejected"
	}
	if r.MintedAt == nil {
		if state.PublishedGeneration == 0 {
			return "none"
		}
		return "pending"
	}
	if r.MintedGeneration != state.PublishedGeneration || r.MintedSHA256 != r.ReportedSHA256 || r.ReportedState != "applied" {
		return "pending"
	}
	if r.MintedKind == domain.DestCandidateEmpty || r.MintedKind == domain.DestCandidatePaused {
		return "none"
	}
	return "applied"
}
