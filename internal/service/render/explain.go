package render

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/rulematch"
)

// ErrExplainUnsupportedClient is returned when the template is not a Mihomo
// one. sing-box would need its compiled route.rules evaluated instead of the
// Clash rule text (and its dropped / PASS-skipped rows reported); that is a
// separate step.
var ErrExplainUnsupportedClient = errors.New("route explanation supports Mihomo templates only")

// Where a rule of the assembled subscription came from.
const (
	ruleSourcePersonal           = "personal"
	ruleSourceSubscriptionDomain = "subscription_domain"
	ruleSourceRuleSet            = "rule_set"
	ruleSourceTemplate           = "template"
)

// Result states, mirroring the browser tester.
const (
	ExplainMatched   = "matched"   // the first matching rule is final
	ExplainContinues = "continues" // it matched, but its target can hand the connection back to the rules
	ExplainUndecided = "undecided" // a line above cannot be decided here
	ExplainNoMatch   = "no_match"  // nothing matches: Mihomo uses DIRECT
)

// ExplainRequest asks where one connection goes in the subscription a real
// client of the template would get.
type ExplainRequest struct {
	TemplateSlug string
	// Draft replaces the stored rule set of the same slug in the template.
	Draft *domain.RuleSet
	// UserID, when set, adds that user's personal rules and resolves line names
	// against the lines that user can see. Without it GroupID (or the default
	// group) supplies the lines and there are no personal rules.
	UserID  int64
	GroupID int64
	Probe   rulematch.ProbeInput
}

// ExplainRuleRef points at one rule of the assembled subscription.
type ExplainRuleRef struct {
	Source  string `json:"source"`
	RuleSet string `json:"rule_set,omitempty"`
	// Line is the 1-based line within the source (the rule set's content, the
	// personal rules, the template); 0 when it could not be located.
	Line   int    `json:"line"`
	Rule   string `json:"rule"`
	Target string `json:"target"`
	Reason string `json:"reason,omitempty"` // undecided lines: why
	// Status for lines below the first match: "unreachable" (the first match is
	// final and nothing undecided stands above it) or "possible".
	Status string `json:"status,omitempty"`
}

// ExplainResult is the answer for the probe.
type ExplainResult struct {
	State string `json:"state"`
	// The first rule that decidably matches (absent for no_match and for an
	// undecided answer with no definite match). For undecided it is only the
	// answer if every undecided line above it turns out not to match.
	*ExplainRuleRef
	Certain bool `json:"certain"`
	// GroupType / GroupDefault / GroupMembers describe the target when it is a proxy group.
	GroupType    string   `json:"group_type,omitempty"`
	GroupDefault string   `json:"group_default,omitempty"`
	GroupMembers []string `json:"group_members,omitempty"`
	// ContinuesVia says why matching may go on below a final-looking match:
	// pass, rematch, udp_unsupported_proxy or unknown_target.
	ContinuesVia string `json:"continues_via,omitempty"`
}

// ExplainResponse is the full answer.
type ExplainResponse struct {
	ClientType   string `json:"client_type"`
	TemplateSlug string `json:"template_slug"`
	GroupID      int64  `json:"group_id"`
	UserID       int64  `json:"user_id,omitempty"`
	// PersonalRules says whether the user's personal rules are part of the walk
	// and why not: "included", "no_user" (no user_id given) or "not_in_template"
	// (the template has no {{ rules_personal }} placeholder).
	PersonalRules   string           `json:"personal_rules"`
	DraftApplied    bool             `json:"draft_applied"`
	Warnings        []string         `json:"warnings,omitempty"`
	Probe           ExplainProbe     `json:"probe"`
	TotalRules      int              `json:"total_rules"`
	DefaultOutbound string           `json:"default_outbound"`
	Result          ExplainResult    `json:"result"`
	Branches        []ExplainRuleRef `json:"branches"`
	BranchesTotal   int              `json:"branches_total"`
	AlsoMatching    []ExplainRuleRef `json:"also_matching"`
	AlsoTotal       int              `json:"also_matching_total"`
	PassSkipped     []ExplainRuleRef `json:"pass_skipped,omitempty"`
}

// ExplainProbe echoes the probe as it was understood.
type ExplainProbe struct {
	Host    string `json:"host"`
	IP      string `json:"ip,omitempty"`
	Port    int    `json:"port,omitempty"`
	Network string `json:"network"`
	Process string `json:"process,omitempty"`
	SrcIP   string `json:"src_ip,omitempty"`
	SrcPort int    `json:"src_port,omitempty"`
}

// ---- marker rules ---------------------------------------------------------------------------------------------------

// The explain render brackets every rule source with a pair of DIRECT rules for
// a reserved .invalid name, so the finished document says where each rule came
// from without re-deriving the order the real render uses. The markers are
// stripped again before anything is matched.
func explainMarker(nonce string, kind byte, index int) string {
	return "DOMAIN,psp-explain-" + nonce + "-" + string(kind) + strconv.Itoa(index) + ".invalid,DIRECT"
}

// newExplainNonce makes the marker names unguessable per call, so no rule a
// user wrote can equal one; markers are also compared whole, never searched for.
func newExplainNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b[:])
}

type explainMarkerKey struct {
	kind  byte
	index int
}

// explainMarkerSet lists every marker rule of one call, for an exact lookup.
func explainMarkerSet(nonce string, segments int) map[string]explainMarkerKey {
	set := make(map[string]explainMarkerKey, 2*(segments+1))
	for i := 0; i <= segments; i++ {
		set[explainMarker(nonce, 'b', i)] = explainMarkerKey{'b', i}
		set[explainMarker(nonce, 'e', i)] = explainMarkerKey{'e', i}
	}
	return set
}

func explainMarkPersonal(nonce, personal string) string {
	body := strings.TrimRight(personal, "\n")
	if body != "" {
		body += "\n"
	}
	return "- " + explainMarker(nonce, 'b', 0) + "\n" + body + "- " + explainMarker(nonce, 'e', 0)
}

func explainMarkSegments(nonce string, segments []ruleSegment) string {
	parts := make([]string, 0, len(segments)*3)
	for i, seg := range segments {
		parts = append(parts, "- "+explainMarker(nonce, 'b', i+1), strings.TrimRight(seg.Content, "\n"), "- "+explainMarker(nonce, 'e', i+1))
	}
	return strings.Join(parts, "\n")
}

// ---- the finished document ---------------------------------------------------------------------------------------------

type explainDocument struct {
	Proxies []struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"proxies"`
	Groups []struct {
		Name                string   `yaml:"name"`
		Type                string   `yaml:"type"`
		Members             []string `yaml:"proxies"`
		Use                 []string `yaml:"use"`
		IncludeAll          bool     `yaml:"include-all"`
		IncludeAllProxies   bool     `yaml:"include-all-proxies"`
		IncludeAllProviders bool     `yaml:"include-all-providers"`
	} `yaml:"proxy-groups"`
	Rules []string `yaml:"rules"`
}

type explainGroup struct {
	typ     string
	members []string
	// extraNodes: the group also takes nodes from providers or include-all.
	extraNodes bool
}

// explainContinuation answers "may matching go on below a rule that names this
// target" from the effective groups of the assembled document, the same
// reasoning the browser tester applies to the inspection's effective members:
// Mihomo follows a group into its current choice and carries on when that is
// PASS or a Rematch outbound (tunnel/tunnel.go match); over UDP it also skips
// a proxy that cannot carry UDP, so any group with a proxy member may continue.
// A subscriber can pick any member of a select group, so a group may continue
// if any member can, directly or through member groups. A target the document
// does not define is skipped by Mihomo (proxies[ada] missing), so it continues.
type explainContinuation struct {
	groups  map[string]explainGroup
	proxies map[string]string
}

func newExplainContinuation(doc *explainDocument) *explainContinuation {
	c := &explainContinuation{groups: map[string]explainGroup{}, proxies: map[string]string{}}
	for _, p := range doc.Proxies {
		c.proxies[p.Name] = strings.ToLower(p.Type)
	}
	for _, g := range doc.Groups {
		c.groups[g.Name] = explainGroup{typ: g.Type, members: g.Members, extraNodes: len(g.Use) > 0 || g.IncludeAll || g.IncludeAllProxies || g.IncludeAllProviders}
	}
	return c
}

func isFinalBuiltin(name string) bool {
	switch strings.ToUpper(name) {
	case "DIRECT", "REJECT", "REJECT-DROP":
		return true
	}
	return false
}

// via returns why matching may go on after target ("" when it cannot).
func (c *explainContinuation) via(target string, udp bool) string {
	return c.walk(target, udp, map[string]bool{})
}

func (c *explainContinuation) walk(name string, udp bool, seen map[string]bool) string {
	if isFinalBuiltin(name) {
		return ""
	}
	if strings.EqualFold(name, "PASS") {
		return "pass"
	}
	if t, ok := c.proxies[name]; ok {
		switch t {
		case "rematch":
			return "rematch"
		case "pass":
			return "pass"
		}
		if udp {
			return "udp_unsupported_proxy"
		}
		return ""
	}
	g, ok := c.groups[name]
	if !ok {
		return "unknown_target"
	}
	if seen[name] {
		return ""
	}
	seen[name] = true
	if udp && g.extraNodes {
		return "udp_unsupported_proxy"
	}
	for _, m := range g.members {
		if v := c.walk(m, udp, seen); v != "" {
			return v
		}
	}
	return ""
}

// ---- source lines ---------------------------------------------------------------------------------------------------------

// entryLines maps the n-th rule of a YAML list text to its 1-based line, or
// returns nil when the text does not read as one rule per "- " line (a multi-
// line scalar, flow style), in which case lines are reported as 0 rather than
// guessed.
func entryLines(content string) []int {
	var lines []int
	for i, raw := range strings.Split(content, "\n") {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if t == "-" || strings.HasPrefix(t, "- ") {
			lines = append(lines, i+1)
		}
	}
	return lines
}

type explainEntry struct {
	text    string
	source  string
	ruleSet string
	line    int
}

// ExplainRoute walks the subscription a Mihomo client of the template would
// get, rule by rule, for one connection.
func (s *Service) ExplainRoute(ctx context.Context, req ExplainRequest) (*ExplainResponse, error) {
	if strings.TrimSpace(req.TemplateSlug) == "" {
		return nil, &domain.CodedValidationError{Code: "explain_template_required", Msg: "template_slug is required"}
	}
	probe, err := rulematch.NewProbe(req.Probe)
	if err != nil {
		var pe *rulematch.ProbeError
		if errors.As(err, &pe) {
			return nil, &domain.CodedValidationError{Code: "explain_" + pe.Code, Params: map[string]string{"field": pe.Field}, Msg: err.Error()}
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	tpl, err := s.repos.Template.GetBySlug(ctx, req.TemplateSlug)
	if err != nil {
		return nil, err
	}
	if tpl.ClientType != domain.ClientMihomo {
		return nil, fmt.Errorf("%w: template %q is %s", ErrExplainUnsupportedClient, tpl.Slug, tpl.ClientType)
	}

	var user domain.User
	resp := &ExplainResponse{ClientType: string(domain.ClientMihomo), TemplateSlug: tpl.Slug, PersonalRules: "no_user"}
	if req.UserID > 0 {
		found, err := s.repos.User.GetByID(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		user = *found
		resp.UserID, resp.PersonalRules = found.ID, "included"
	} else {
		gid := req.GroupID
		if gid <= 0 {
			if st, err := s.repos.Settings.Load(ctx, renderSettingsDefaults()); err == nil {
				gid = st.RegistrationDefaultGroupID
			}
		}
		if gid <= 0 {
			groups, err := s.repos.Group.List(ctx)
			if err != nil {
				return nil, err
			}
			if len(groups) == 0 {
				return nil, &domain.CodedValidationError{Code: "explain_no_group", Msg: "no group exists to resolve lines from"}
			}
			gid = groups[0].ID
		}
		// No PSPClient is read and no user is impersonated: the values only let
		// the renderer produce the document.
		user = domain.User{GroupID: gid, UUID: "00000000-0000-4000-8000-000000000001", UPN: "route-explain", DisplayName: "route-explain"}
	}
	resp.GroupID = user.GroupID

	pc := &previewContext{template: tpl, explain: true, nonce: newExplainNonce()}
	if req.Draft != nil {
		if req.Draft.Slug == "" {
			return nil, &domain.CodedValidationError{Code: "explain_draft_slug_required", Msg: "draft needs a slug"}
		}
		pc.ruleSets = []*domain.RuleSet{req.Draft}
		inTemplate := false
		for _, slug := range tpl.RuleSets {
			inTemplate = inTemplate || slug == req.Draft.Slug
		}
		resp.DraftApplied = inTemplate
		if !inTemplate {
			resp.Warnings = append(resp.Warnings, "draft_not_in_template")
		}
	}
	output, err := s.renderForContext(ctx, &user, domain.ClientMihomo, pc)
	if err != nil {
		return nil, err
	}
	var doc explainDocument
	if err := yaml.Unmarshal(output.Body, &doc); err != nil {
		return nil, fmt.Errorf("parse assembled subscription: %w", err)
	}

	// Attribute every rule of the finished document to its source.
	type segState struct {
		count   int
		entries []int // indexes into entries
	}
	segs := make([]segState, len(pc.segments)+1)
	markers := explainMarkerSet(pc.nonce, len(pc.segments))
	var entries []explainEntry
	current := -1
	personalPlaced := false
	for _, text := range doc.Rules {
		if m, ok := markers[text]; ok {
			if m.kind == 'b' {
				current = m.index
				personalPlaced = personalPlaced || m.index == 0
			} else {
				current = -1
			}
			continue
		}
		e := explainEntry{text: text, source: ruleSourceTemplate}
		if current >= 0 && current < len(segs) {
			segs[current].count++
			segs[current].entries = append(segs[current].entries, len(entries))
			switch {
			case current == 0:
				e.source = ruleSourcePersonal
			case pc.segments[current-1].Kind == ruleSourceSubscriptionDomain:
				e.source = ruleSourceSubscriptionDomain
				e.line = 1
			default:
				e.source, e.ruleSet = ruleSourceRuleSet, pc.segments[current-1].Slug
			}
		}
		entries = append(entries, e)
	}
	if req.UserID > 0 && !personalPlaced {
		resp.PersonalRules = "not_in_template"
	}
	for idx, st := range segs {
		var content string
		switch {
		case idx == 0:
			content = user.PersonalRules
		case pc.segments[idx-1].Kind == ruleSourceRuleSet:
			content = pc.segments[idx-1].Content
		default:
			continue
		}
		if lines := entryLines(content); len(lines) == st.count {
			for k, ei := range st.entries {
				entries[ei].line = lines[k]
			}
		}
	}
	// Rules the template itself carries: find them in its text.
	if tplLines := strings.Split(tpl.Content, "\n"); len(tplLines) > 0 {
		used := map[int]bool{}
		for i := range entries {
			if entries[i].source != ruleSourceTemplate {
				continue
			}
			for ln, raw := range tplLines {
				t := strings.TrimSpace(raw)
				if !used[ln] && (t == "- "+entries[i].text || t == "- '"+entries[i].text+"'" || t == "- \""+entries[i].text+"\"") {
					used[ln] = true
					entries[i].line = ln + 1
					break
				}
			}
		}
	}

	texts := make([]string, len(entries))
	for i, e := range entries {
		texts[i] = e.text
	}
	cont := newExplainContinuation(&doc)
	route := rulematch.Walk(texts, probe, func(target string, udp bool) bool {
		return cont.via(target, udp) != ""
	})

	ref := func(h rulematch.Hit) ExplainRuleRef {
		e := entries[h.Index]
		return ExplainRuleRef{Source: e.source, RuleSet: e.ruleSet, Line: e.line, Rule: e.text, Target: h.Target, Reason: string(h.Reason)}
	}
	resp.TotalRules = len(entries)
	resp.DefaultOutbound = "DIRECT"
	resp.Probe = ExplainProbe{Host: probe.Host, Port: probe.Port, Network: probe.Network, Process: probe.Process, SrcPort: probe.SrcPort}
	if probe.Resolved.IsValid() {
		resp.Probe.IP = probe.Resolved.String()
	}
	if probe.SrcIP.IsValid() {
		resp.Probe.SrcIP = probe.SrcIP.String()
	}
	resp.Branches, resp.BranchesTotal = []ExplainRuleRef{}, route.MaybeTotal
	for _, h := range route.Maybe {
		resp.Branches = append(resp.Branches, ref(h))
	}
	for _, h := range route.Passed {
		resp.PassSkipped = append(resp.PassSkipped, ref(h))
	}
	undecided := route.MaybeTotal > 0
	resp.AlsoMatching, resp.AlsoTotal = []ExplainRuleRef{}, route.LaterTotal
	for _, h := range route.Later {
		r := ref(h)
		r.Status = "possible"
		if route.First != nil && route.Final && !undecided {
			r.Status = "unreachable"
		}
		resp.AlsoMatching = append(resp.AlsoMatching, r)
	}

	res := &resp.Result
	if route.First != nil {
		r := ref(*route.First)
		res.ExplainRuleRef = &r
		if g, ok := cont.groups[r.Target]; ok {
			res.GroupType = g.typ
			if len(g.members) > 50 {
				res.GroupMembers = g.members[:50]
			} else {
				res.GroupMembers = g.members
			}
			if strings.EqualFold(g.typ, "select") && len(g.members) > 0 {
				res.GroupDefault = g.members[0]
			}
		}
		if !route.Final {
			res.ContinuesVia = cont.via(r.Target, probe.Network == "udp")
		}
	}
	switch {
	case undecided:
		res.State = ExplainUndecided
	case route.First == nil:
		res.State = ExplainNoMatch
		res.Certain = true
	case !route.Final:
		res.State = ExplainContinues
		res.Certain = true
	default:
		res.State = ExplainMatched
		res.Certain = true
	}
	return resp, nil
}
