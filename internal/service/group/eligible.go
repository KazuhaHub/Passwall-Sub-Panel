package group

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// eligibilityTTL bounds how stale a cached panel eligibility may be. Every
// event that changes the answer invalidates it explicitly (InvalidateEligibility);
// the TTL is the backstop for one that does not.
const eligibilityTTL = 30 * time.Second

// PanelReader, AgentReader and PolicyStateReader are the three reads an
// allowlist group's eligibility rests on. They are narrow on purpose: this
// package must not grow a dependency on the destination-policy service.
type PanelReader interface {
	GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
}

// AgentReader reads a native panel's agent (its observed capabilities and the
// core engine it is configured to run).
type AgentReader interface {
	GetByPanelID(ctx context.Context, panelID int64) (*domain.NodeAgent, error)
}

// PolicyStateReader reads an agent's destination-policy delivery state, of
// which only the fallback reason is used.
type PolicyStateReader interface {
	GetMeta(ctx context.Context, agentID string) (*domain.DestAgentPolicy, error)
}

// EligibilityDeps is what Eligible reads beyond the group and the node. Any
// field left nil turns the allowlist test off: no destination policy can be
// compiled without them either, so Eligible then equals Matches.
type EligibilityDeps struct {
	Panels   PanelReader
	Agents   AgentReader
	Policies PolicyStateReader
}

func (d EligibilityDeps) wired() bool {
	return d.Panels != nil && d.Agents != nil && d.Policies != nil
}

// MemberResyncer re-derives the node memberships of a group's members
// (user.Service).
type MemberResyncer interface {
	ResyncGroupMembersInBackground(groupID int64)
}

// SetEligibilityDeps late-binds the eligibility reads (the destination family
// is built after this service).
func (s *Service) SetEligibilityDeps(d EligibilityDeps) { s.elig = d }

// SetMemberResyncer late-binds the resyncer ResyncAllowlistGroups drives.
func (s *Service) SetMemberResyncer(r MemberResyncer) { s.resyncer = r }

// allowlisted reports whether the group's destination allowlist is in force:
// report and on both are (R5: from the first report the group's members get no
// client on a node that cannot enforce it, or its report would miss them).
func allowlisted(g *domain.Group) bool {
	if g == nil {
		return false
	}
	switch g.DestAllowlist.State {
	case domain.DestStateReport, domain.DestStateOn:
		return true
	}
	return false
}

// Eligible reports whether the group's members may have a client on n: the one
// test the whole code base shares for "does this group include this node".
//
// A group with no allowlist is exactly Matches(n, g.TagFilter). An allowlisted
// group also needs a node that can enforce it: a native panel whose agent
// declares the destination policy and group capabilities, whose policy was not
// refused or over a limit, and, on Xray, whose enabled inbounds can supply the
// sniffing the group's domain rules need.
//
// The sniffing is judged on the inbounds themselves and not by the policy's
// "sniffing" fallback reason: that fallback depends on the roster, which
// excluding the members changes, so judging by it would flip the answer on
// every sync (ineligible, members removed, rules gone, precheck passes,
// eligible, members added, precheck fails).
//
// A failed read is an error and never a verdict.
func (s *Service) Eligible(ctx context.Context, n *domain.Node, g *domain.Group) (bool, error) {
	if !Matches(n, g.TagFilter) {
		return false, nil
	}
	if !allowlisted(g) || !s.elig.wired() {
		return true, nil
	}
	return s.panelCanEnforce(ctx, n.PanelID)
}

// panelCanEnforce is the allowlist half of Eligible, which depends on the
// panel only and so is cached per panel.
func (s *Service) panelCanEnforce(ctx context.Context, panelID int64) (bool, error) {
	now := s.clock()
	if ok, hit := s.eligCache.get(panelID, now); hit {
		return ok, nil
	}
	version := s.eligCache.currentVersion()
	ok, err := s.readPanelCanEnforce(ctx, panelID)
	if err != nil {
		return false, err
	}
	s.eligCache.putIfVersion(panelID, ok, now, version)
	return ok, nil
}

func (s *Service) readPanelCanEnforce(ctx context.Context, panelID int64) (bool, error) {
	reason, err := s.PanelEnforceReason(ctx, panelID)
	return reason == "" && err == nil, err
}

// Why a panel cannot enforce an allowlist group.
const (
	EnforceXUI        = "xui"
	EnforceSUI        = "sui"
	EnforceCapability = "capability"
	EnforceOverLimit  = "over_limit"
	EnforceRejected   = "rejected"
	EnforceSniffing   = "sniffing"
)

// PanelEnforceReason is why a panel cannot enforce an allowlist group, "" when
// it can: the answer Eligible gives, with its reason (the admin API tells an
// administrator why a node is not open to the group). A failed read is an
// error and never a verdict. It reads the panel, agent and policy rows each
// call; Eligible is the cached hot path.
func (s *Service) PanelEnforceReason(ctx context.Context, panelID int64) (string, error) {
	panel, err := s.elig.Panels.GetByID(ctx, panelID)
	if errors.Is(err, domain.ErrNotFound) {
		return EnforceCapability, nil
	}
	if err != nil {
		return "", err
	}
	switch domain.NormalizePanelKind(panel.Kind) {
	case domain.PanelKindPSP:
	case domain.PanelKindSUI:
		return EnforceSUI, nil
	default:
		return EnforceXUI, nil
	}
	agent, err := s.elig.Agents.GetByPanelID(ctx, panelID)
	if errors.Is(err, domain.ErrNotFound) {
		return EnforceCapability, nil
	}
	if err != nil {
		return "", err
	}
	if !slices.Contains(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) ||
		!slices.Contains(agent.ObservedCapabilities, protocol.CapabilityDestinationGroups) {
		return EnforceCapability, nil
	}
	meta, err := s.elig.Policies.GetMeta(ctx, agent.AgentID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		// No delivery state yet: nothing was refused or over a limit.
	case err != nil:
		return "", err
	case meta.FallbackReason == domain.DestFallbackRejected:
		return EnforceRejected, nil
	case meta.FallbackReason == domain.DestFallbackOverLimit:
		return EnforceOverLimit, nil
	}
	if domain.NormalizeNodeCoreEngine(agent.DesiredCoreEngine) != domain.NodeCoreXray {
		// sing-box takes its sniff rule from the route, which the node writes.
		return "", nil
	}
	nodes, err := s.listEnabledCached(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if n.PanelID != panelID || !n.Enabled || n.IsSeparator() {
			continue
		}
		// An allowlist is a domain allowlist, so the need is always domains.
		verdict, _, err := protocol.CheckXraySniffing([]byte(n.Sniffing), protocol.SniffForDomains)
		if err != nil || verdict == protocol.SniffInsufficient {
			// A sniffing object that does not parse is as unusable as an
			// insufficient one: the policy compile reports it the same way.
			return EnforceSniffing, nil
		}
	}
	return "", nil
}

// PanelsFor are the panels a group's tags select, by id: the ones with an
// enabled inbound the filter matches.
func (s *Service) PanelsFor(ctx context.Context, g *domain.Group) ([]int64, error) {
	nodes, err := s.listEnabledCached(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	var out []int64
	for _, n := range nodes {
		if n.IsSeparator() || seen[n.PanelID] || !Matches(n, g.TagFilter) {
			continue
		}
		seen[n.PanelID] = true
		out = append(out, n.PanelID)
	}
	slices.Sort(out)
	return out, nil
}

// TagMatchedMembers counts the members of a group that a panel's tags select,
// whether or not they are eligible for it or have a client on it now. The
// per-node rule limit counts these and not the roster, which shrinks when the
// members are made ineligible and would put the limit back under on the next
// compile (destpolicy.GroupMemberCounter).
//
// It runs before the compile cache key on every sync of every node, so the
// member count is cached per group for the eligibility TTL.
func (s *Service) TagMatchedMembers(ctx context.Context, panelID, groupID int64) (int, error) {
	entry, err := s.groupMembership(ctx, groupID)
	if err != nil {
		return 0, err
	}
	nodes, err := s.listEnabledCached(ctx)
	if err != nil {
		return 0, err
	}
	for _, n := range nodes {
		if n.PanelID == panelID && !n.IsSeparator() && Matches(n, entry.filter) {
			return entry.members, nil
		}
	}
	return 0, nil
}

// groupMembership is a group's tag filter and member count, read together and
// cached for the eligibility TTL (Update clears it).
func (s *Service) groupMembership(ctx context.Context, groupID int64) (memberCountEntry, error) {
	now := s.clock()
	s.countMu.Lock()
	if e, ok := s.counts[groupID]; ok && now.Before(e.expires) {
		s.countMu.Unlock()
		return e, nil
	}
	version := s.countVersion
	s.countMu.Unlock()
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return memberCountEntry{}, err
	}
	n, err := s.groups.CountMembers(ctx, groupID)
	if err != nil {
		return memberCountEntry{}, err
	}
	e := memberCountEntry{filter: g.TagFilter, members: int(n), expires: now.Add(eligibilityTTL)}
	s.countMu.Lock()
	if s.countVersion == version { // a read that began before an invalidation must not put its answer back
		if s.counts == nil {
			s.counts = map[int64]memberCountEntry{}
		}
		s.counts[groupID] = e
	}
	s.countMu.Unlock()
	return e, nil
}

type memberCountEntry struct {
	filter  domain.TagFilter
	members int
	expires time.Time
}

// InvalidateEligibility drops the cached eligibility of one panel, or of every
// panel when panelID is 0 (an allowlist's state changed).
func (s *Service) InvalidateEligibility(panelID int64) {
	if s != nil {
		s.eligCache.invalidate(panelID)
		if panelID == 0 {
			// The allowlist's state or the group itself changed: the cached tag
			// filters and member counts go with the eligibility.
			s.InvalidateMembers()
		}
	}
}

// InvalidateMembers drops the cached tag filters and member counts, for when a
// member joins or leaves a group (the user service tells it).
func (s *Service) InvalidateMembers() {
	if s == nil {
		return
	}
	s.countMu.Lock()
	clear(s.counts)
	s.countVersion++
	s.countMu.Unlock()
}

// ResyncAllowlistGroups is what a change in what a panel can enforce triggers:
// it drops the panel's cached eligibility and then re-derives the memberships
// of every group that has an allowlist. The order is required: the resync
// reads Eligible, which would otherwise answer from the cache the change made
// stale. It returns how many groups it resynced. With no resyncer wired it only
// invalidates (the heal sweep converges the members).
func (s *Service) ResyncAllowlistGroups(ctx context.Context, panelID int64) int {
	s.InvalidateEligibility(panelID)
	if s.resyncer == nil {
		return 0
	}
	groups, err := s.groups.List(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, g := range groups {
		if allowlisted(g) {
			s.resyncer.ResyncGroupMembersInBackground(g.ID)
			n++
		}
	}
	return n
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// MatchOnly is the Eligible of a service with no allowlist knowledge: exactly
// Matches. Consumers use it until the real service is wired, so that tests and
// early start behave as the code did before allowlist groups existed.
type MatchOnly struct{}

// Eligible implements the consumers' eligibility interface.
func (MatchOnly) Eligible(_ context.Context, n *domain.Node, g *domain.Group) (bool, error) {
	return Matches(n, g.TagFilter), nil
}

// eligibilityCache memoizes a panel's can-enforce answer. A read that began
// before an invalidation cannot put its answer back (version).
type eligibilityCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[int64]eligibilityEntry
	version uint64
}

type eligibilityEntry struct {
	ok      bool
	expires time.Time
}

func newEligibilityCache(ttl time.Duration) *eligibilityCache {
	return &eligibilityCache{ttl: ttl, entries: map[int64]eligibilityEntry{}}
}

func (c *eligibilityCache) get(panelID int64, now time.Time) (ok, hit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.entries[panelID]
	if !found || !now.Before(e.expires) {
		return false, false
	}
	return e.ok, true
}

func (c *eligibilityCache) currentVersion() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

func (c *eligibilityCache) putIfVersion(panelID int64, ok bool, now time.Time, version uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != version {
		return false
	}
	c.entries[panelID] = eligibilityEntry{ok: ok, expires: now.Add(c.ttl)}
	return true
}

// invalidate drops one panel, or all of them for panelID 0. Every call moves
// the version, so reads in flight cannot repopulate what was just dropped.
func (c *eligibilityCache) invalidate(panelID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	if panelID == 0 {
		clear(c.entries)
		return
	}
	delete(c.entries, panelID)
}

// InboundsChanged is what an edit to a panel's inbounds (their sniffing, or
// whether they are enabled) triggers. Only a native panel can enforce an
// allowlist, so only for one does the change matter; its eligibility is then
// re-read and the allowlist groups' members resynced.
func (s *Service) InboundsChanged(ctx context.Context, panelID int64) int {
	if s == nil || s.elig.Panels == nil {
		return 0
	}
	panel, err := s.elig.Panels.GetByID(ctx, panelID)
	if err != nil || domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		return 0
	}
	return s.ResyncAllowlistGroups(ctx, panelID)
}

// Wired reports whether the eligibility reads and the member resyncer have been
// bound. It exists for the application's wiring test: an unbound dependency
// degrades to "tags only" without a word.
func (s *Service) Wired() (eligibility, resyncer bool) {
	return s.elig.wired(), s.resyncer != nil
}

// AllowlistExcludedPanels are the panels the group's allowlist keeps its
// members off: those that have an enabled node the group's tags select but
// cannot enforce the allowlist. A panel outside the tags is not one (moving a
// group from it is an ordinary move), and a group with no allowlist excludes
// none.
func (s *Service) AllowlistExcludedPanels(ctx context.Context, g *domain.Group) (map[int64]bool, error) {
	out := map[int64]bool{}
	if !allowlisted(g) || !s.elig.wired() {
		return out, nil
	}
	nodes, err := s.listEnabledCached(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.IsSeparator() || !Matches(n, g.TagFilter) {
			continue
		}
		if _, seen := out[n.PanelID]; seen {
			continue
		}
		ok, err := s.panelCanEnforce(ctx, n.PanelID)
		if err != nil {
			return nil, err
		}
		if !ok {
			out[n.PanelID] = true
		}
	}
	return out, nil
}
