package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// The admin-side of the policy service: validating what an admin submits,
// measuring it against the limits, reading the delivery status and answering
// "what would happen to this connection". The stores each of them touches are
// declared here, narrow, and satisfied by the concrete repos.

// PolicyStore is dest_policies (sqlstore.DestPolicyRepo).
type PolicyStore interface {
	Create(ctx context.Context, p *domain.DestPolicy) error
	GetByID(ctx context.Context, id int64) (*domain.DestPolicy, error)
	List(ctx context.Context) ([]*domain.DestPolicy, error)
	Update(ctx context.Context, p *domain.DestPolicy, ifUpdatedAt *time.Time) error
	Delete(ctx context.Context, id int64) error
	Reorder(ctx context.Context, action domain.DestAction, ids []int64) error
}

// ListReader reads lists for validation (sqlstore.DestListRepo).
type ListReader interface {
	GetByID(ctx context.Context, id int64) (*domain.DestList, error)
}

// GroupCounter counts accounts per group, for the subject estimate.
type GroupCounter interface {
	CountUsersByGroup(ctx context.Context) (map[int64]int, error)
}

// Existence checks for what a policy references.
type GroupLookup interface {
	GetByID(ctx context.Context, id int64) (*domain.Group, error)
}

// PanelLookup reads panels.
type PanelLookup interface {
	GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
	List(ctx context.Context) ([]*domain.XUIPanel, error)
}

// NodeLookup reads one inbound.
type NodeLookup interface {
	GetByID(ctx context.Context, id int64) (*domain.Node, error)
}

// ClientLookup lists a user's clients, to find the panel to test against.
type ClientLookup interface {
	ListByUser(ctx context.Context, userID int64) ([]*domain.PSPClient, error)
}

// AgentLister lists the native agents (ports.NodeAgentRepo).
type AgentLister interface {
	List(ctx context.Context) ([]*domain.NodeAgent, error)
}

// DesiredReader loads a native panel's inbounds and roster.
type DesiredReader interface {
	Load(ctx context.Context, panelID int64) (*ports.NativeDesiredSnapshot, error)
}

// AdminDeps is everything an Admin is built from. Service, Policies, Lists,
// Panels and Nodes are required; the others may be nil and the feature each
// serves then degrades (no Groups: a group id is not checked; no Counter: the
// subject budget counts exemptions only; no Desired: the test tool cannot use a
// panel's roster; no Agents: the status lists no node).
type AdminDeps struct {
	Service  *Service
	Policies PolicyStore
	Lists    ListReader
	Counter  GroupCounter
	Groups   GroupLookup
	Panels   PanelLookup
	Nodes    NodeLookup
	Clients  ClientLookup
	Agents   AgentLister
	Desired  DesiredReader
	Settings SettingsReader
	// Hits counts the hits each panel produced recently (sqlstore.DestHitRepo).
	// Nil: hits_24h is null.
	Hits HitCounter
	// Instantiate creates lists and a policy in one transaction
	// (sqlstore.DestRepos.InstantiateTemplate); the global exception needs it.
	Instantiate func(ctx context.Context, lists []domain.DestList, p *domain.DestPolicy) (*domain.DestPolicy, error)
	// ExceptionEditor edits the exception list's text (destlist.Service).
	ExceptionEditor ListEditor
	Now             func() time.Time
}

// HitCounter counts a panel's hits since an instant (PanelCounts).
type HitCounter interface {
	PanelCounts(ctx context.Context, sinceMS int64) (map[int64]int64, error)
}

// ListEditor adds entries to a custom list (destlist.Service.AddEntries).
type ListEditor interface {
	AddEntries(ctx context.Context, id int64, add, remove []string) (*domain.DestList, *destlist.ParseReport, error)
}

// Admin is the admin-side service.
type Admin struct {
	AdminDeps

	budgetMu    sync.Mutex
	budgetGen   int64
	budgetAt    time.Time
	budgetCache *Budget
}

// NewAdmin builds the admin service.
func NewAdmin(d AdminDeps) (*Admin, error) {
	if d.Service == nil || d.Policies == nil || d.Lists == nil || d.Panels == nil || d.Nodes == nil {
		return nil, errors.New("destpolicy: service, policies, lists, panels and nodes are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Admin{AdminDeps: d}, nil
}

// ---- policies ----

// PolicyInput is the whole of a policy as an admin submits it.
type PolicyInput struct {
	Name        string
	Description string
	Action      domain.DestAction
	State       domain.DestState
	ListIDs     []int64
	Inline      domain.DestPolicyInline
	Scope       domain.DestScope
	GroupIDs    []int64
	PanelIDs    []int64
	TemplateKey string
	// CountsAsRisk says the policy's enforced blocks feed the dest_block risk
	// signal. Only a block policy may set it (a report-only block may store it:
	// its report rows never count, it counts once enforced). Nil on a create is
	// false; nil on an edit keeps the stored value, so a client that does not
	// know the field cannot clear it by omission.
	CountsAsRisk *bool
}

// PreparePolicy validates and normalises an admin's policy and returns the row
// to store (existing, when editing, supplies the identity and the template
// key, which an edit cannot change). Every refusal is a *ValidationError with
// the code the API answers with.
func (a *Admin) PreparePolicy(ctx context.Context, in PolicyInput, existing *domain.DestPolicy) (*domain.DestPolicy, error) {
	bad := func(field, msg string, vals ...string) error {
		return &ValidationError{Code: CodePolicyInvalid, Field: field, Bad: vals, Msg: msg}
	}
	switch {
	case !domain.ValidDestName(in.Name):
		return nil, bad("name", fmt.Sprintf("the name must be 1..%d bytes without surrounding space", domain.DestNameMax))
	case len(in.Description) > domain.DestDescriptionMax:
		return nil, bad("description", fmt.Sprintf("the description is over %d bytes", domain.DestDescriptionMax))
	case !in.Action.Valid():
		return nil, bad("action", "action is block or allow", string(in.Action))
	case !in.Scope.Valid():
		return nil, bad("scope", "scope is all, groups or panels", string(in.Scope))
	}
	if err := CheckPolicyState(in.Action, in.State); err != nil {
		return nil, err
	}
	inline, verr := normalizeInline(in.Inline)
	if verr != nil {
		return nil, verr
	}
	if in.Action == domain.DestActionAllow && len(inline.Protocols) > 0 {
		return nil, bad("inline.protocols", "an allow policy cannot match a protocol")
	}
	// Only a request that creates an allow policy, or keeps one an allow, may
	// not carry the flag. A block that becomes an allow in the same request is a
	// client echoing the whole DTO back: the flag is dropped below, not refused.
	if in.CountsAsRisk != nil && *in.CountsAsRisk && in.Action != domain.DestActionBlock &&
		(existing == nil || existing.Action == in.Action) {
		return nil, bad("counts_as_risk", "only a block policy can count as a risk signal", "true")
	}
	p := &domain.DestPolicy{
		Name: in.Name, Description: in.Description, Action: in.Action, State: in.State, Inline: inline, Scope: in.Scope,
		ListIDs: uniqueSorted(in.ListIDs),
	}
	if existing != nil {
		p.ID, p.Priority, p.CreatedAt, p.UpdatedAt, p.TemplateKey = existing.ID, existing.Priority, existing.CreatedAt, existing.UpdatedAt, existing.TemplateKey
		p.CountsAsRisk = existing.CountsAsRisk
	}
	if in.CountsAsRisk != nil {
		p.CountsAsRisk = *in.CountsAsRisk
	}
	if p.Action != domain.DestActionBlock {
		// An allow records nothing, so there is nothing to count: a block turned
		// into an allow without saying so does not keep a flag it cannot use.
		p.CountsAsRisk = false
	}
	if existing == nil && len(in.TemplateKey) > 0 {
		return nil, bad("template_key", "a policy is added from a template through the catalog, not created with a key")
	}
	for _, id := range p.ListIDs {
		l, err := a.Lists.GetByID(ctx, id)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			return nil, bad("list_ids", "no such list", fmt.Sprint(id))
		case err != nil:
			return nil, err
		case l.Side != domain.DestSideDest:
			return nil, bad("list_ids", "a source list cannot be used by a destination policy", fmt.Sprint(id))
		case l.OwnerGroupID != 0:
			return nil, bad("list_ids", "a list that belongs to a group cannot be used by another policy", fmt.Sprint(id))
		}
	}
	switch in.Scope {
	case domain.DestScopeGroups:
		p.GroupIDs = uniqueSorted(in.GroupIDs)
		if len(p.GroupIDs) == 0 {
			return nil, bad("group_ids", "scope groups needs at least one group")
		}
		if a.Groups != nil {
			for _, id := range p.GroupIDs {
				if _, err := a.Groups.GetByID(ctx, id); errors.Is(err, domain.ErrNotFound) {
					return nil, bad("group_ids", "no such group", fmt.Sprint(id))
				} else if err != nil {
					return nil, err
				}
			}
		}
	case domain.DestScopePanels:
		p.PanelIDs = uniqueSorted(in.PanelIDs)
		if len(p.PanelIDs) == 0 {
			return nil, bad("panel_ids", "scope panels needs at least one panel")
		}
		for _, id := range p.PanelIDs {
			panel, err := a.Panels.GetByID(ctx, id)
			switch {
			case errors.Is(err, domain.ErrNotFound):
				return nil, bad("panel_ids", "no such panel", fmt.Sprint(id))
			case err != nil:
				return nil, err
			case domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP:
				return nil, bad("panel_ids", "only a native node enforces a policy", fmt.Sprint(id))
			}
		}
	}
	if existing != nil && existing.TemplateKey != "" {
		// A rule group from the catalog keeps its mapping: the name, the
		// description, the state and the scope are the admin's, what it matches
		// is the catalog's. To match something else, copy it into a custom list
		// and make a policy of one's own.
		if p.Action != existing.Action || !slices.Equal(p.ListIDs, existing.ListIDs) || !inlineEqual(p.Inline, existing.Inline) {
			return nil, bad("template_key", "a rule group from the catalog cannot change what it matches; copy it into a custom policy instead")
		}
	}
	if len(p.ListIDs) == 0 && inline.Empty() {
		return nil, &ValidationError{Code: CodePolicyNoMatch, Field: "list_ids", Msg: "the policy matches nothing: add a list or a built-in match"}
	}
	return p, nil
}

// inlineEqual compares two built-in matches by what they say (nil and empty
// slices are the same).
func inlineEqual(a, b domain.DestPolicyInline) bool {
	return slices.Equal(a.CIDRs, b.CIDRs) && a.Ports == b.Ports && a.Network == b.Network &&
		slices.Equal(a.Protocols, b.Protocols) && a.Private == b.Private
}

func uniqueSorted(in []int64) []int64 {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// ---- budget ----

// BudgetItem is one limit's use.
type BudgetItem struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

// Budget is how much of each limit the definitions use, measured the way the
// publish measures them: on the upper bound that does not depend on the node.
// Subjects is an estimate: the accounts of the groups a policy is scoped to,
// plus the exemptions; the exact figure is per node and is judged there.
type Budget struct {
	Rules       BudgetItem `json:"rules"`
	Domains     BudgetItem `json:"domains"`
	Regexps     BudgetItem `json:"regexps"`
	CIDRs       BudgetItem `json:"cidrs"`
	SourceCIDRs BudgetItem `json:"source_cidrs"`
	Subjects    BudgetItem `json:"subjects"`
	Bytes       BudgetItem `json:"bytes"`
}

func budgetOf(u Usage) *Budget {
	return &Budget{
		Rules: BudgetItem{u.Rules, Limits.Rules}, Domains: BudgetItem{u.Domains, Limits.Domains},
		Regexps: BudgetItem{u.Regexps, Limits.Regexps}, CIDRs: BudgetItem{u.CIDRs, Limits.CIDRs},
		SourceCIDRs: BudgetItem{u.SourceCIDRs, Limits.SourceCIDRs}, Subjects: BudgetItem{u.Subjects, Limits.Subjects},
		Bytes: BudgetItem{u.Bytes, Limits.Bytes},
	}
}

// budgetTTL bounds how stale the cached budget of the stored definitions can
// be. It is keyed by generation too, so any write invalidates it at once; the
// TTL only covers what the generation does not see (a geosite or GeoIP change
// that advanced nothing).
const budgetTTL = 10 * time.Second

// Budget measures the stored definitions, or — with hypo — the definitions as
// they would be if hypo were saved (replacing the policy with its id, or added
// when it has none). A refused hypothetical save is what the 400 over-limit
// answers carry; the stored figure is what the lists page shows.
func (a *Admin) Budget(ctx context.Context, hypo *domain.DestPolicy) (*Budget, error) {
	return a.budget(ctx, hypo, GroupHypo{})
}

// BudgetWithGroup is Budget with an allowlist group saved as hypothesised.
func (a *Admin) BudgetWithGroup(ctx context.Context, h GroupHypo) (*Budget, error) {
	return a.budget(ctx, nil, h)
}

func (a *Admin) budget(ctx context.Context, hypo *domain.DestPolicy, gh GroupHypo) (*Budget, error) {
	cacheable := hypo == nil && gh.Mode == nil
	if cacheable {
		a.budgetMu.Lock()
		cached, gen, at := a.budgetCache, a.budgetGen, a.budgetAt
		a.budgetMu.Unlock()
		if st, err := a.Service.state.Get(ctx); err == nil && cached != nil && gen == st.Generation && a.Now().Sub(at) < budgetTTL {
			return cached, nil
		}
	}
	defs, err := a.Service.defs.ReadDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	if hypo != nil {
		replaced := false
		for i, p := range defs.Policies {
			if p.ID == hypo.ID && hypo.ID != 0 {
				defs.Policies[i], replaced = hypo, true
			}
		}
		if !replaced {
			defs.Policies = append(defs.Policies, hypo)
		}
	}
	if gh.Mode != nil {
		defs.GroupModes = slices.DeleteFunc(slices.Clone(defs.GroupModes), func(m *domain.DestGroupMode) bool { return m.GroupID == gh.Mode.GroupID })
		defs.GroupModes = append(defs.GroupModes, gh.Mode)
		defs.Lists = append(slices.Clone(defs.Lists), gh.Lists...)
	}
	st, err := a.Service.state.Get(ctx)
	if err != nil {
		return nil, err
	}
	doc := newSnapshotDoc(defs, st, a.Now())
	b, err := a.Service.buildBase(ctx, doc)
	if err != nil {
		return nil, err
	}
	var usage Usage
	if p := b.assemble(nodeView{bound: true}); p != nil {
		usage = usageOf(p)
	}
	usage.Subjects = len(doc.Exempt)
	// The allowlist groups: each member once per rule the group has on a node
	// (the upper bound has no roster, so its synthetic subjects are not counted).
	groupRules := map[int64]int{}
	if p := b.assemble(nodeView{bound: true}); p != nil {
		for _, r := range p.Rules {
			if kind, gid, ok := ruleOrigin(r.ID); ok && kind == 'g' {
				groupRules[gid]++
			}
		}
	}
	if a.Counter != nil {
		counts, err := a.Counter.CountUsersByGroup(ctx)
		if err != nil {
			return nil, err
		}
		seen := map[int64]bool{}
		for _, p := range doc.Policy {
			for _, g := range p.Groups {
				if !seen[g] {
					seen[g] = true
					usage.Subjects += counts[g]
				}
			}
		}
		for gid, n := range groupRules {
			usage.Subjects += counts[gid] * n
		}
	}
	out := budgetOf(usage)
	if cacheable {
		a.budgetMu.Lock()
		a.budgetCache, a.budgetGen, a.budgetAt = out, defs.Generation, a.Now()
		a.budgetMu.Unlock()
	}
	return out, nil
}

// CheckBudget refuses a save that would put the definitions over a limit that
// the publish would refuse anyway (dest_policy_over_limit, with which limit,
// how much, against what). A publish refusal then only arises when several
// individually legal writes add up.
func (a *Admin) CheckBudget(ctx context.Context, hypo *domain.DestPolicy) error {
	b, err := a.Budget(ctx, hypo)
	if err != nil {
		return err
	}
	return b.over()
}

// over is the first limit the budget passes, as the API answers it.
func (b *Budget) over() error {
	for _, c := range []struct {
		kind string
		item BudgetItem
	}{
		{"rules", b.Rules}, {"domains", b.Domains}, {"regexps", b.Regexps}, {"cidrs", b.CIDRs},
		{"source_cidrs", b.SourceCIDRs}, {"subjects", b.Subjects}, {"bytes", b.Bytes},
	} {
		if c.item.Used > c.item.Limit {
			return &ValidationError{Code: CodePolicyOverLimit, Field: c.kind, Kind: c.kind, Used: c.item.Used, Limit: c.item.Limit,
				Msg: fmt.Sprintf("the policy would use %d %s of %d allowed", c.item.Used, c.kind, c.item.Limit)}
		}
	}
	return nil
}

// ---- exemptions and source rules ----

// ValidateExemption checks an exemption's reason and expiry.
func ValidateExemption(reason string, expiresAt *time.Time, now time.Time) error {
	if len(reason) > maxExemptionReasonBytes {
		return &ValidationError{Code: CodeExemptionInvalid, Field: "reason", Msg: fmt.Sprintf("the reason is over %d bytes", maxExemptionReasonBytes)}
	}
	if expiresAt != nil && !expiresAt.After(now) {
		return &ValidationError{Code: CodeExemptionInvalid, Field: "expires_at", Msg: "the expiry is already past"}
	}
	return nil
}

// ErrSourceRuleNotNative is a source rule for an inbound whose panel is not a
// native node (HTTP 409 dest_source_rule_not_native): only a native node
// enforces one.
var ErrSourceRuleNotNative = fmt.Errorf("%w: a source rule needs a native node", domain.ErrConflict)

// SourceRuleInput is what an admin submits for one inbound.
type SourceRuleInput struct {
	Countries []string
	CIDRs     []string
	State     domain.DestState
}

// PrepareSourceRule validates and normalises a source rule for node nodeID. It
// answers ErrSourceRuleNotNative for an inbound that is not on a native node
// and ErrNotFound for one that does not exist.
func (a *Admin) PrepareSourceRule(ctx context.Context, nodeID int64, in SourceRuleInput) (*domain.DestSourceRule, error) {
	node, err := a.Nodes.GetByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	panel, err := a.Panels.GetByID(ctx, node.PanelID)
	if err != nil {
		return nil, err
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		return nil, ErrSourceRuleNotNative
	}
	if err := CheckState(in.State); err != nil {
		return nil, err
	}
	countries, ok := domain.NormalizeDestCountries(in.Countries)
	if !ok {
		return nil, &ValidationError{Code: CodeSourceRuleInvalid, Field: "countries", Bad: in.Countries, Msg: "countries are ISO 3166-1 alpha-2 codes"}
	}
	if len(countries) > maxSourceRuleCountries {
		return nil, &ValidationError{Code: CodeSourceRuleInvalid, Field: "countries", Msg: "too many countries"}
	}
	cidrs, badCIDRs := NormalizeCIDRs(in.CIDRs)
	if len(badCIDRs) > 0 {
		return nil, &ValidationError{Code: CodeSourceRuleInvalid, Field: "cidrs", Bad: badCIDRs, Msg: "not a CIDR prefix"}
	}
	if len(cidrs) > maxSourceRuleCIDRs {
		return nil, &ValidationError{Code: CodeSourceRuleInvalid, Field: "cidrs", Msg: "too many prefixes"}
	}
	return &domain.DestSourceRule{NodeID: nodeID, Countries: countries, CIDRs: cidrs, State: in.State}, nil
}

// ---- the test tool ----

// TestInput is a connection to evaluate.
type TestInput struct {
	Target        string
	Port          int
	Network       string
	UserID        int64
	PanelID       int64
	InboundNodeID int64
	SourceIP      string
	// ResolvedIPs are the addresses the admin says the target resolves to, for a
	// node that resolves hostnames at routing time (on_demand). nil is "not
	// given" (the answer for a hostname is then conditional); an empty list is a
	// resolution that failed. At most maxResolvedIPs address literals. PSP never
	// looks the name up itself: its resolver is not the node's.
	ResolvedIPs []string
}

// maxResolvedIPs bounds the addresses a test may carry.
const maxResolvedIPs = 16

// TestStep is one line of the trace.
type TestStep struct {
	// Step is the 1-based position.
	Step   int    `json:"step"`
	RuleID string `json:"rule_id,omitempty"`
	// Kind is "source", "allow", "block", "exempt" or "builtin".
	Kind string `json:"kind"`
	// ReportOnly is true on a step whose rule is report-only (a policy or a
	// source rule in the report state): the step records, and a hit on it does
	// not block. Kind stays what the rule is (block, allow, source).
	ReportOnly bool   `json:"report_only"`
	PolicyID   int64  `json:"policy_id,omitempty"`
	NodeID     int64  `json:"node_id,omitempty"`
	Name       string `json:"name,omitempty"`
	// Result is "hit", "miss", "untestable", "shadowed" or "skipped".
	Result  string `json:"result"`
	Entry   string `json:"entry,omitempty"`
	Builtin string `json:"builtin,omitempty"`
}

// TestNode says which node the evaluation was made against.
type TestNode struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name"`
	State   string `json:"state"`
}

// TestResult is the evaluation.
type TestResult struct {
	Verdict string `json:"verdict"`
	// TerminatingStep is the rule id that ended the evaluation, "exempt" for
	// the exempt list, the builtin's name for a builtin, "" for no hit.
	TerminatingStep string     `json:"terminating_step"`
	Steps           []TestStep `json:"steps"`
	Notes           []string   `json:"notes"`
	// Builtin names the node-internal rule that decided it ("trailing_dot"),
	// "" when none did.
	Builtin     string     `json:"builtin,omitempty"`
	Conditional bool       `json:"conditional"`
	Unpublished bool       `json:"unpublished"`
	Nodes       []TestNode `json:"nodes"`
}

// Test evaluates one connection against the PUBLISHED policy, specialised for a
// node: the given panel, else the panel of the given inbound, else the first
// native panel the given user has a client on, else none (rules that name
// groups or panels are then not in play, and a connection by nobody is not
// exempt and in no group). It does protocol.MatchDestination over the same
// document the node would be sent, and does no name resolution.
func (a *Admin) Test(ctx context.Context, in TestInput) (*TestResult, error) {
	target := strings.TrimSpace(in.Target)
	switch {
	case target == "" || len(target) > maxExceptionTargetBytes+2:
		return nil, &ValidationError{Code: CodeTestInvalid, Field: "target", Msg: "the target is a host name or an address"}
	case in.Port < 0 || in.Port > 65535:
		return nil, &ValidationError{Code: CodeTestInvalid, Field: "port", Msg: "the port is 1..65535"}
	case in.Network != "" && in.Network != "tcp" && in.Network != "udp":
		return nil, &ValidationError{Code: CodeTestInvalid, Field: "network", Msg: "the network is tcp or udp"}
	}
	if err := validateResolvedIPs(in.ResolvedIPs); err != nil {
		return nil, err
	}
	st, err := a.Service.state.Get(ctx)
	if err != nil {
		return nil, err
	}
	res := &TestResult{Steps: []TestStep{}, Notes: []string{}, Nodes: []TestNode{}, Unpublished: st.Unpublished()}

	panelID := in.PanelID
	if panelID == 0 && in.InboundNodeID != 0 {
		if n, err := a.Nodes.GetByID(ctx, in.InboundNodeID); err == nil {
			panelID = n.PanelID
		}
	}
	if panelID == 0 && in.UserID != 0 && a.Clients != nil {
		clients, err := a.Clients.ListByUser(ctx, in.UserID)
		if err != nil {
			return nil, err
		}
		slices.SortFunc(clients, func(x, y *domain.PSPClient) int { return int(x.PanelID - y.PanelID) })
		for _, c := range clients {
			if p, err := a.Panels.GetByID(ctx, c.PanelID); err == nil && domain.NormalizePanelKind(p.Kind) == domain.PanelKindPSP {
				panelID = c.PanelID
				break
			}
		}
	}

	var policy *protocol.DestinationPolicy
	var names map[string]TestStep
	var snapNodes []*domain.Node
	if st.PublishedGeneration > 0 {
		cb, err := a.Service.baseFor(ctx, st.PublishedGeneration)
		if err != nil {
			return nil, err
		}
		view := nodeView{nodeIDs: map[int64]bool{}, groupCap: true}
		if in.InboundNodeID != 0 {
			view.nodeIDs[in.InboundNodeID] = true
		}
		if panelID != 0 && a.Desired != nil {
			snap, err := a.Desired.Load(ctx, panelID)
			if err != nil {
				return nil, err
			}
			view = nodeView{panelID: panelID, nodeIDs: map[int64]bool{}, groupCap: true}
			for _, n := range snap.Nodes {
				if n != nil {
					view.nodeIDs[n.ID] = true
					snapNodes = append(snapNodes, n)
				}
			}
			seen := map[int64]bool{}
			for _, c := range snap.Clients {
				if c.Client != nil && !seen[c.Client.UserID] {
					seen[c.Client.UserID] = true
					view.users = append(view.users, c.Client.UserID)
				}
			}
			if cb.base.needsGroups(true) {
				if view.groupOf, err = a.Service.defs.GroupIDsByUserIDs(ctx, view.users); err != nil {
					return nil, err
				}
			}
			if p, err := a.Panels.GetByID(ctx, panelID); err == nil {
				res.Nodes = append(res.Nodes, TestNode{PanelID: panelID, Name: p.Name, State: "published"})
			}
		}
		policy = cb.base.assemble(view)
		if len(snapNodes) > 0 {
			// What this node is actually sent: the rules that need the sniffer run
			// only on the inbounds that can supply it.
			policy, _ = restrictForSniffing(policy, snapNodes, a.engineOf(ctx, panelID))
		}
		// How the node resolves a hostname against IP rules: the mode it would run,
		// the panel's choice as the node's capabilities and inbounds allow it.
		if panelID != 0 {
			a.applyResolve(ctx, policy, panelID, snapNodes)
		}
		names = describeRules(cb)
	}

	input := protocol.MatchInput{Target: target, Port: uint16(in.Port), Network: in.Network, Source: strings.TrimSpace(in.SourceIP)}
	if in.UserID != 0 {
		input.Subject = protocol.NewSubjectKey(in.UserID)
	}
	if in.InboundNodeID != 0 {
		input.Inbound = protocol.NewListenerKey(in.InboundNodeID)
	}
	input.Resolved = in.ResolvedIPs
	m := protocol.MatchDestination(policy, input)
	res.Verdict, res.Conditional = string(m.Verdict), m.Conditional
	switch {
	case m.ExemptHit:
		res.TerminatingStep = "exempt"
	case m.Terminated != "":
		res.TerminatingStep = m.Terminated
	default:
		for _, s := range m.Steps {
			if s.Result == protocol.StepHit && s.Builtin != "" {
				res.TerminatingStep, res.Builtin = string(s.Builtin), string(s.Builtin)
			}
		}
	}
	for i, s := range m.Steps {
		step := TestStep{Step: i + 1, RuleID: s.RuleID, Result: string(s.Result), Entry: s.Entry, Builtin: string(s.Builtin)}
		switch {
		case s.Exempt:
			step.Kind = "exempt"
		case s.Builtin != "":
			step.Kind = "builtin"
		default:
			info := stepInfo(names, s.RuleID)
			step.Kind, step.ReportOnly, step.PolicyID, step.NodeID, step.Name = info.Kind, info.ReportOnly, info.PolicyID, info.NodeID, info.Name
		}
		res.Steps = append(res.Steps, step)
	}
	for _, n := range m.Notes {
		res.Notes = append(res.Notes, string(n))
	}
	return res, nil
}

// GroupHypo is an allowlist group as it would be after a save: its state and
// lists, and the lists that do not exist yet (a new group's own), under ids the
// store has not assigned.
type GroupHypo struct {
	Mode  *domain.DestGroupMode
	Lists []*domain.DestList
}

// CheckGroupBudget refuses a group save that would put the definitions over a
// limit (dest_policy_over_limit), as CheckBudget does for a policy.
func (a *Admin) CheckGroupBudget(ctx context.Context, h GroupHypo) error {
	b, err := a.BudgetWithGroup(ctx, h)
	if err != nil {
		return err
	}
	return b.over()
}
