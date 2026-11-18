// Package nodesync owns PSP's side of the native node protocol. It is the one
// minter for config, roster and directives, and the one ingestion point for
// agent observations. adapters/pspnode projects its latest full report into
// the legacy PanelClient port; it never invents observed state from intent.
package nodesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sourceaddr"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/linkprobe"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodemetrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/route"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sourceguard"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

const (
	// Tasks is the last SyncResponse field and omitempty removes it from the
	// no-task encoding. Its exact JSON field overhead is therefore this suffix.
	syncTasksFieldJSONBytes = len(`,"tasks":`)
)

type Service struct {
	desired  ports.NativeDesiredSnapshotRepo
	agents   ports.NodeAgentRepo
	issues   ports.NodeAgentIssueRepo
	tasks    ports.NodeAgentTaskRepo
	users    ports.UserRepo
	clients  ports.PSPClientRepo
	nodes    ports.NodeRepo
	settings ports.ScopedSettings
	// orphanSeen counts, per tunnel listener, the syncs in a row that found no line
	// for it (see orphanTunnelIssues).
	orphanMu   sync.Mutex
	orphanSeen map[int64]int
	panels     ports.XUIPanelRepo
	// coreCatalog is the review the configured core is re-checked against.
	coreCatalog ports.CoreCatalog

	agentLocks keyedmutex.Map[string]

	mu      sync.RWMutex
	reports map[string]receivedFullReport
	anchors map[int64]nodeprotocol.ClientCounters
	grants  map[string]map[nodeprotocol.ClientKey]int64
	now     func() time.Time
	// invalidateRender is late-bound because the native adapter needs this
	// service before the panel pool (and therefore render service) can exist.
	invalidateRender func()
	// normalizeRealityFingerprints is late-bound for the same assembly-order
	// reason. Every trusted native Xray observation invokes it idempotently so
	// upgrades, first boot on an already-new core, and prior partial failures all
	// converge without relying on a one-shot version transition.
	normalizeRealityFingerprints func(context.Context, int64, string) (int, error)
	// host ingests the optional telemetry subtree. NIL MEANS THIS BUILD DOES NOT
	// COLLECT IT, which is what keeps a panel with no metrics repository from
	// advertising a cadence it cannot honour.
	host *nodemetrics.Service
	// policies decides the destination policy a node runs. NIL MEANS THIS BUILD
	// HAS NO ACCESS CONTROL: the sync is then exactly what it was before the
	// feature existed. It is late-bound (SetPolicies) because the compiler needs
	// the GeoIP and list services, which are built after this one.
	policies DestPolicyCompiler
	// allowlistResync runs when a panel's ability to enforce a group allowlist
	// changed (SetAllowlistResyncer).
	allowlistResync func(ctx context.Context, panelID int64)
	// audit takes the node's hit batches for storage. NIL MEANS THIS BUILD DOES
	// NOT RECORD HITS: a batch a node reports is then simply not kept. It is
	// late-bound (SetAudit) like the policy compiler.
	audit AuditSink
	// configCache holds, per agent, the canonical bytes of the last config
	// that carried a policy, keyed by what they were built from: the policy
	// is up to several MiB and re-encoding it on every thirty-second sync of
	// every node is the cost this avoids.
	configCache map[string]configCacheEntry
	// transit publishes managed relay lines to the servers that run them and
	// records what they confirm. NIL MEANS THIS BUILD HAS NO MANAGED RELAYS:
	// no config carries Transit, so every config is what it was before.
	transit TransitPublisher
	// probes decides what an entry server probes and takes what it measured.
	// NIL MEANS THIS BUILD DOES NOT PROBE: no config carries a probe block.
	probes ProbeCoordinator
	// fronts plans the SNI-shared ports of a server (transit.sni.v1). NIL
	// MEANS THIS BUILD DOES NOT SHARE PORTS: no config carries Fronts.
	fronts FrontPublisher
	routes RoutePlanner
	// guard decides the source guard sent to a landing server that can enforce
	// one. NIL MEANS THIS BUILD DOES NOT GUARD: no set, no guard.
	guard SourceGuardPlanner
	hits  SourceGuardHitStore
}

// FrontPublisher is the SNI front collaborator (service/front.Service).
type FrontPublisher interface {
	// PlanFor is the fronts a server is sent: empty unless the agent declared
	// transit.sni.v1, its desired engine is Xray and it reports Xray running.
	// present are the nodes in its config.
	PlanFor(ctx context.Context, panelID int64, capabilities []string, desired, observed domain.NodeCoreEngine, present []int64) (front.Plan, error)
	// UnservedListeners are the listeners that must not be sent because their
	// front is not being sent and they would bind one port together.
	UnservedListeners(ctx context.Context, panelID int64, plan front.Plan, present []int64) ([]front.Unserved, error)
}

// IssueTunnelListenerOrphan is the issue recorded for a hidden tunnel listener
// whose line is gone (a delete that was cut short): it is still sent, serves
// nobody, and can be removed from the server's port usage or its node page.
const IssueTunnelListenerOrphan = "tunnel_listener_orphan"

// IssueTunnelFactorStale is the issue recorded for a tunnel listener whose copy
// of its line's traffic factor differs from the line's.
const IssueTunnelFactorStale = "tunnel_factor_stale"

// IssueCDNLandingUnfit is the issue recorded for a node whose CDN lines cannot
// work any more because its transport no longer suits a CDN (changed outside PSP,
// or adopted from a panel). The lines are withheld from subscriptions.
const IssueCDNLandingUnfit = "cdn_landing_unfit"

// IssueSourceGuardWithheld is the issue recorded for a relay line that is left
// out of subscriptions because its landing cannot tell it from the direct line
// (its traffic factor differs and the landing cannot check where connections
// come from), or because it can and the line's source addresses are not known.
const IssueSourceGuardWithheld = "source_guard_withheld"

// IssueSourceGuardRefused is the issue recorded for a source address a landing
// refused for a line (a connection the line's identity made from an address
// that is not among the line's source addresses). The administrator can add
// the address to the line.
const IssueSourceGuardRefused = "source_guard_refused"

const rosterIdentityWarningThreshold = 20_000

func rosterCapacityIssues(snapshot *ports.NativeDesiredSnapshot) []domain.NodeAgentIssue {
	count := 0
	for _, desired := range snapshot.Clients {
		if desired.Client != nil {
			count++
		}
	}
	if count < rosterIdentityWarningThreshold {
		return nil
	}
	return []domain.NodeAgentIssue{{
		Code:   "roster_identity_capacity",
		Detail: fmt.Sprintf("Server roster reached %d client identities. The sync byte limit still applies.", rosterIdentityWarningThreshold),
	}}
}

// cdnUnfitIssues names the CDN lines of the snapshot's nodes that are withheld
// because their landing cannot go through a CDN, with the reason.
func cdnUnfitIssues(snapshot *ports.NativeDesiredSnapshot) []domain.NodeAgentIssue {
	var out []domain.NodeAgentIssue
	for _, node := range snapshot.Nodes {
		if node == nil || node.IsTunnelChild() {
			continue
		}
		fault := domain.CDNLandingFault(node)
		if fault == nil || fault.Code == domain.CDNFaultUnreadable {
			continue
		}
		problem := domain.CDNLandingProblem(node)
		var lines []string
		for _, line := range node.Relays {
			if line.CDN() {
				lines = append(lines, fmt.Sprintf("%q", line.Label()))
			}
		}
		if len(lines) > 0 {
			out = append(out, domain.NodeAgentIssue{
				Code: IssueCDNLandingUnfit, Key: string(nodeprotocol.NewListenerKey(node.ID)),
				Detail: fmt.Sprintf("CDN line %s of %s is withheld from subscriptions: %s", strings.Join(lines, ", "), node.DisplayName, problem),
			})
		}
	}
	return out
}

// sourceGuardIssues names the relay lines the source guard withholds, with the
// reason.
func sourceGuardIssues(withheld []sourceguard.Withheld) []domain.NodeAgentIssue {
	var out []domain.NodeAgentIssue
	for _, w := range withheld {
		var detail string
		switch w.Reason {
		case domain.SourceWithheldFactor:
			detail = fmt.Sprintf("line %q of %s costs %d where the direct line costs %d, and this server cannot check where connections come from, so the line is withheld from subscriptions. Give the line the direct line's factor, or use a Passwall Node that supports source checks.",
				w.Line.Label(), w.Node.DisplayName, w.Line.EffectiveTrafficFactor(), w.Node.EffectiveTrafficFactor())
		case domain.SourceWithheldUnknown:
			detail = fmt.Sprintf("line %q of %s is withheld from subscriptions: the addresses its traffic reaches this server from are not known yet. Add them to the line's source addresses.",
				w.Line.Label(), w.Node.DisplayName)
		default:
			continue
		}
		out = append(out, domain.NodeAgentIssue{
			Code: IssueSourceGuardWithheld, Key: string(nodeprotocol.NewListenerKey(w.Node.ID)), Detail: detail,
		})
	}
	return out
}

// orphanTunnelIssues names the tunnel listeners of the snapshot that no line of
// an existing landing points at.
func (s *Service) orphanTunnelIssues(ctx context.Context, snapshot *ports.NativeDesiredSnapshot) []domain.NodeAgentIssue {
	s.pruneOrphans(ctx, snapshot)
	var out []domain.NodeAgentIssue
	for _, node := range snapshot.Nodes {
		if node == nil || !node.IsTunnelChild() || s.nodes == nil {
			continue
		}
		parent, err := s.nodes.GetByID(ctx, node.ParentNodeID)
		if err == nil && tunnelLineOf(parent, node) {
			s.clearOrphan(node.ID)
			// The listener carries a copy of its line's factor, which the quota
			// grants of this server read: a copy that differs is reported.
			for _, line := range parent.Relays {
				if line.ID == node.ParentLineID && line.EffectiveTrafficFactor() != node.EffectiveTrafficFactor() {
					out = append(out, domain.NodeAgentIssue{
						Code: IssueTunnelFactorStale, Key: string(nodeprotocol.NewListenerKey(node.ID)),
						Detail: fmt.Sprintf("%s carries traffic factor %d but its line's is %d: save the line's factor again",
							node.DisplayName, node.EffectiveTrafficFactor(), line.EffectiveTrafficFactor()),
					})
				}
			}
			continue
		} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
			continue // unreadable is not orphaned
		}
		// A listener is made, and the line pointed at it, by one save under the
		// servers' locks; a sync that reads between the two would take a listener
		// being made for an orphan. So an orphan is reported only once it has been
		// seen as one by two syncs in a row.
		if !s.confirmOrphan(node.ID) {
			continue
		}
		out = append(out, domain.NodeAgentIssue{
			Code: IssueTunnelListenerOrphan, Key: string(nodeprotocol.NewListenerKey(node.ID)),
			Detail: fmt.Sprintf("%s belongs to a tunnel line that no longer exists: remove it", node.DisplayName),
		})
	}
	return out
}

// confirmOrphan counts the syncs that saw a listener as an orphan and says
// whether it is the second in a row (true), which is when it is reported.
func (s *Service) confirmOrphan(id int64) bool {
	s.orphanMu.Lock()
	defer s.orphanMu.Unlock()
	if s.orphanSeen == nil {
		s.orphanSeen = map[int64]int{}
	}
	s.orphanSeen[id]++
	return s.orphanSeen[id] >= 2
}

// pruneOrphans forgets the count of every listener that no longer exists, so
// the map cannot grow with listeners that were removed.
func (s *Service) pruneOrphans(ctx context.Context, snapshot *ports.NativeDesiredSnapshot) {
	inSnapshot := map[int64]bool{}
	for _, n := range snapshot.Nodes {
		if n != nil {
			inSnapshot[n.ID] = true
		}
	}
	s.orphanMu.Lock()
	ids := make([]int64, 0, len(s.orphanSeen))
	for id := range s.orphanSeen {
		if !inSnapshot[id] {
			ids = append(ids, id)
		}
	}
	s.orphanMu.Unlock()
	if s.nodes == nil {
		return
	}
	for _, id := range ids {
		if _, err := s.nodes.GetByID(ctx, id); errors.Is(err, domain.ErrNotFound) {
			s.clearOrphan(id)
		}
	}
}

// clearOrphan forgets the count of a listener that is not an orphan.
func (s *Service) clearOrphan(id int64) {
	s.orphanMu.Lock()
	delete(s.orphanSeen, id)
	s.orphanMu.Unlock()
}

// tunnelLineOf reports whether a line of the landing is served by the listener.
func tunnelLineOf(landing, child *domain.Node) bool {
	for _, line := range landing.Relays {
		if line.ID == child.ParentLineID && line.Tunnel() && line.TunnelNodeID == child.ID {
			return true
		}
	}
	return false
}

// IssueFrontWithheld is the issue recorded for a saved front that is not being
// sent; its key is the front's key and its detail says why.
const IssueFrontWithheld = "front_withheld"

// withoutNodes is the snapshot without the nodes of the listeners left out.
func withoutNodes(snapshot *ports.NativeDesiredSnapshot, left []route.Withheld) *ports.NativeDesiredSnapshot {
	drop := make(map[int64]bool, len(left))
	for _, w := range left {
		drop[w.NodeID] = true
	}
	clone := *snapshot
	clone.Nodes = nil
	for _, node := range snapshot.Nodes {
		if node != nil && !drop[node.ID] {
			clone.Nodes = append(clone.Nodes, node)
		}
	}
	return &clone
}

// presentNodes are the nodes in a server's desired config.
func presentNodes(snapshot *ports.NativeDesiredSnapshot) []int64 {
	out := make([]int64, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node != nil {
			out = append(out, node.ID)
		}
	}
	return out
}

// frontContractFaults says which fronts the contract refuses, and why. Each is
// checked on its own: alone with the forwards it routes to, then next to
// everything else in the body that is not a front. One bad front is the only one
// withheld, and a fault that belongs to some other object (a link) is charged to
// none: the body without any front must itself be accepted for a front to be
// blamed. The fronts that pass are finally checked together, which catches two
// of them clashing; those are then all withheld.
func frontContractFaults(body nodeprotocol.Transit, plan front.Plan) map[nodeprotocol.FrontKey]string {
	faulty := map[nodeprotocol.FrontKey]string{}
	forwards := map[nodeprotocol.ForwardKey]nodeprotocol.Forward{}
	for _, forward := range plan.Forwards {
		forwards[forward.Key] = forward
	}
	forwardsOf := func(f nodeprotocol.Front) []nodeprotocol.Forward {
		var out []nodeprotocol.Forward
		for _, route := range f.Routes {
			if forward, ok := forwards[route.Target.Forward]; ok && route.Target.Forward != "" {
				out = append(out, forward)
			}
		}
		return out
	}
	fronted := map[nodeprotocol.LinkKey]bool{}
	for _, in := range plan.LinksIn {
		fronted[in.Key] = true
	}
	links := map[nodeprotocol.LinkKey]nodeprotocol.LinkIn{}
	for _, in := range body.LinksIn {
		if fronted[in.Key] {
			links[in.Key] = in
		}
	}
	linksOf := func(f nodeprotocol.Front) []nodeprotocol.LinkIn {
		var out []nodeprotocol.LinkIn
		for _, route := range f.Routes {
			if in, ok := links[route.Target.Link]; ok {
				out = append(out, in)
			}
		}
		return out
	}
	base := body
	base.LinksIn = slices.DeleteFunc(slices.Clone(body.LinksIn), func(in nodeprotocol.LinkIn) bool {
		return fronted[in.Key]
	})
	base.Fronts = nil
	base.Forwards = nil
	for _, forward := range body.Forwards {
		if !plan.FrontedForwards[forward.Key] {
			base.Forwards = append(base.Forwards, forward)
		}
	}
	baseOK := nodeprotocol.ValidateTransit(&base) == nil
	var remaining []nodeprotocol.Front
	for _, f := range plan.Fronts {
		alone := nodeprotocol.Transit{Fronts: []nodeprotocol.Front{f}, Forwards: forwardsOf(f), LinksIn: linksOf(f)}
		if err := nodeprotocol.ValidateTransit(&alone); err != nil {
			faulty[f.Key] = err.Error()
			continue
		}
		if baseOK {
			next := base
			next.Fronts = []nodeprotocol.Front{f}
			next.Forwards = append(slices.Clone(base.Forwards), forwardsOf(f)...)
			next.LinksIn = append(slices.Clone(base.LinksIn), linksOf(f)...)
			if err := nodeprotocol.ValidateTransit(&next); err != nil {
				faulty[f.Key] = err.Error()
				continue
			}
		}
		remaining = append(remaining, f)
	}
	if baseOK && len(remaining) > 1 {
		together := base
		together.Fronts = remaining
		for _, f := range remaining {
			together.Forwards = append(together.Forwards, forwardsOf(f)...)
			together.LinksIn = append(together.LinksIn, linksOf(f)...)
		}
		if err := nodeprotocol.ValidateTransit(&together); err != nil {
			for _, f := range remaining {
				faulty[f.Key] = err.Error()
			}
		}
	}
	return faulty
}

// SetFronts binds the SNI front planner. Left unbound, no config carries
// fronts.
func (s *Service) SetFronts(f FrontPublisher) { s.fronts = f }

// TransitPublisher is the managed relay collaborator (service/transit.Service).
type TransitPublisher interface {
	ForwardsWithProbeMethods(ctx context.Context, entryPanelID int64) ([]nodeprotocol.Forward, map[nodeprotocol.ForwardKey]string, error)
	Confirm(ctx context.Context, entryPanelID int64, held *nodeprotocol.Transit, statuses map[string]nodeprotocol.ObjectStatus, now time.Time) (bool, error)
	RecordCounters(ctx context.Context, sent *nodeprotocol.Transit, counters []nodeprotocol.ForwardCounters) error
	LinksForFronts(ctx context.Context, panelID int64, occupied map[int]bool, routes bool, fronted []nodeprotocol.LinkIn) (transit.LinkPlan, error)
	WithholdIncomingLinks(ctx context.Context, links []nodeprotocol.LinkKey) error
	ConfirmLinks(ctx context.Context, held *nodeprotocol.Transit, statuses map[string]nodeprotocol.ObjectStatus) (bool, error)
	RecordLinkCounters(ctx context.Context, sent *nodeprotocol.Transit, counters []nodeprotocol.LinkCounters) error
}

// RoutePlanner is the routing-by-destination collaborator (service/route.Service).
type RoutePlanner interface {
	// PlanFor compiles the routes of the nodes a server is sent: empty unless
	// the agent declared transit.route.v1 and transit.link.v1.
	PlanFor(ctx context.Context, in route.PlanInput) (route.Plan, error)
	// RecordSent stores what the config holds of each node's routes.
	RecordSent(ctx context.Context, nodes []*domain.Node, sent []nodeprotocol.ListenerRoutes, withheld map[int64]string) (bool, error)
	// Early decides which listeners cannot be sent with their routes before
	// shared ports and links are planned.
	Early(ctx context.Context, panelID int64, caps []string, nodes []*domain.Node) (route.Plan, error)
	// Confirm records which routes the server confirmed holding.
	Confirm(ctx context.Context, held *nodeprotocol.Transit, statuses map[string]nodeprotocol.ObjectStatus, now time.Time) (bool, error)
}

// SourceGuardPlanner decides the source sets and client guards of a landing
// server (service/sourceguard.Planner).
type SourceGuardPlanner interface {
	PlanFor(ctx context.Context, capabilities []string, nodes []*domain.Node) (sourceguard.Plan, error)
	WithheldLines(ctx context.Context, capabilities []string, nodes []*domain.Node) ([]sourceguard.Withheld, error)
}

// SetSourceGuard binds the source guard planner. Left unbound, no config
// carries a source set and no roster a guard.
func (s *Service) SetSourceGuard(g SourceGuardPlanner) { s.guard = g }

// SourceGuardHitStore keeps the sources a landing refused.
type SourceGuardHitStore interface {
	RecordHits(ctx context.Context, panelID int64, hits []domain.SourceGuardHit, cutoff time.Time, limit int) error
}

// SetSourceGuardHits binds where refused sources are kept. Left unbound, they
// are not recorded.
func (s *Service) SetSourceGuardHits(store SourceGuardHitStore) { s.hits = store }

// How long, and how many, refused sources a server keeps.
const (
	sourceGuardHitRetention = 30 * 24 * time.Hour
	sourceGuardHitLimit     = 1000
)

// SetRoutes binds the routing planner. Left unbound, no config carries routes.
func (s *Service) SetRoutes(r RoutePlanner) { s.routes = r }

// ProbeCoordinator is the link-probe collaborator (service/linkprobe.Service).
type ProbeCoordinator interface {
	// Config is the probe block for a server's transit body, nil when it must
	// not be sent (no capability, probing off, nothing to probe).
	Config(ctx context.Context, capabilities []string, transit *nodeprotocol.Transit, methods map[nodeprotocol.ForwardKey]string) (*nodeprotocol.ProbeSettings, error)
	// Ingest books the probe results of a full report against the config that
	// was sent, and reports whether a withheld flag changed.
	Ingest(ctx context.Context, panelID int64, capabilities []string, sent *nodeprotocol.Transit, results []nodeprotocol.ProbeResult, agentReportedMS int64) (bool, error)
}

// SetProbes binds the link-probe coordinator. Left unbound, no probe block is
// sent and no result is read.
func (s *Service) SetProbes(p ProbeCoordinator) { s.probes = p }

// SetTransit binds the managed relay publisher. Left unbound, no forwards are
// published and managed relay lines stay withheld from subscriptions.
func (s *Service) SetTransit(t TransitPublisher) { s.transit = t }

// AuditSink is the hit-recording collaborator (service/destaudit.Service).
type AuditSink interface {
	// Enqueue offers one validated batch for storage. It does no database work
	// and never blocks: it is called while the node waits for its response.
	Enqueue(agentID string, panelID int64, capabilities []string, observation *nodeprotocol.AuditObservation)
}

// DestPolicyCompiler is the destination-policy collaborator
// (service/destpolicy.Service).
type DestPolicyCompiler interface {
	// ObserveStatus takes what the node reports about the policy it runs. It
	// is called BEFORE the node's config is built.
	ObserveStatus(ctx context.Context, agentID string, capabilities []string, status *nodeprotocol.PolicyStatus) ([]domain.NodeAgentIssue, error)
	// Compile produces the policy for the config about to be sent. It is
	// called AFTER the config (without a policy) is built.
	Compile(ctx context.Context, in destpolicy.CompileInput) (destpolicy.Result, error)
}

type configCacheEntry struct {
	key       string
	canonical []byte
}

// PruneAgents drops the cached config of every agent not in live.
func (s *Service) PruneAgents(live map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.configCache {
		if !live[id] {
			delete(s.configCache, id)
		}
	}
}

// SetAudit binds the hit sink. Left unbound, hit batches are dropped.
func (s *Service) SetAudit(a AuditSink) { s.audit = a }

// SetAllowlistResyncer binds what runs when a native panel starts or stops
// being able to enforce a group allowlist: it drops that panel's cached
// eligibility and then resyncs the members of every allowlist group
// (group.Service.ResyncAllowlistGroups). The destination-policy service shares
// it for the fallback reasons that matter. Left unbound nothing is triggered
// and the heal sweep converges the members.
func (s *Service) SetAllowlistResyncer(resync func(ctx context.Context, panelID int64)) {
	s.allowlistResync = resync
}

// allowlistCapabilities are the capabilities a panel's eligibility for an
// allowlist rests on. Task capabilities change from report to report, so the
// whole set must not be compared.
var allowlistCapabilities = []string{nodeprotocol.CapabilityDestinationPolicy, nodeprotocol.CapabilityDestinationGroups}

// allowlistCapabilitiesChanged reports whether either capability appeared or
// disappeared between two reports.
func allowlistCapabilitiesChanged(before, after []string) bool {
	for _, c := range allowlistCapabilities {
		if slices.Contains(before, c) != slices.Contains(after, c) {
			return true
		}
	}
	return false
}

// SetPolicies binds the destination-policy compiler. Left unbound, nodes are
// never sent a policy.
func (s *Service) SetPolicies(p DestPolicyCompiler) { s.policies = p }

// receivedFullReport keeps the control plane's receipt time beside the latest
// full enumeration. ReportedAtMS is useful for clock-skew diagnostics, but it
// is agent-controlled and therefore must never decide freshness, coverage, or
// an authorization bound.
type receivedFullReport struct {
	report       nodeprotocol.NodeReport
	receivedAtMS int64
}

type Options struct {
	Desired  ports.NativeDesiredSnapshotRepo
	Agents   ports.NodeAgentRepo
	Issues   ports.NodeAgentIssueRepo
	Tasks    ports.NodeAgentTaskRepo
	Users    ports.UserRepo
	Clients  ports.PSPClientRepo
	Nodes    ports.NodeRepo
	Settings ports.ScopedSettings
	Panels   ports.XUIPanelRepo
	// CoreCatalog resolves the node's configured core release against the review
	// that is current now. Required: a config body that cannot be checked against
	// the review is not one the panel should dispatch.
	CoreCatalog ports.CoreCatalog
	// Host is optional: a build without a metrics repository simply does not
	// ingest telemetry, and the envelope carries no cadence for it.
	Host *nodemetrics.Service
	Now  func() time.Time
}

func New(options Options) (*Service, error) {
	if options.Desired == nil || options.Agents == nil || options.Issues == nil || options.Tasks == nil || options.Users == nil ||
		options.Clients == nil || options.Nodes == nil || options.Settings == nil {
		return nil, errors.New("nodesync: desired, agents, issues, tasks, users, clients, nodes, settings are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		desired: options.Desired, agents: options.Agents, issues: options.Issues, tasks: options.Tasks, users: options.Users,
		clients: options.Clients, nodes: options.Nodes, settings: options.Settings,
		panels: options.Panels, coreCatalog: options.CoreCatalog,
		reports: make(map[string]receivedFullReport),
		anchors: make(map[int64]nodeprotocol.ClientCounters), now: now,
		grants: make(map[string]map[nodeprotocol.ClientKey]int64),
		host:   options.Host,
	}, nil
}

func (s *Service) SetRenderInvalidator(invalidate func()) { s.invalidateRender = invalidate }

func (s *Service) SetRealityFingerprintNormalizer(normalize func(context.Context, int64, string) (int, error)) {
	s.normalizeRealityFingerprints = normalize
}

// Sync ingests one agent observation and returns all three independently
// conditional segments in the same round trip. Authentication stays outside
// this service: the production HTTP boundary resolves a strict Bearer digest
// to the same agent ID before calling this coordinator.
func (s *Service) Sync(ctx context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
	return s.SyncWithResponseEncoding(ctx, report, "")
}

// SyncWithResponseEncoding budgets the response for the coding negotiated by HTTP.
func (s *Service) SyncWithResponseEncoding(ctx context.Context, report nodeprotocol.NodeReport, encoding string) (nodeprotocol.SyncResponse, error) {
	responseLimit := nodeprotocol.MaxSyncWireBytes
	if encoding == nodeprotocol.SyncEncodingGzip && slices.Contains(report.Capabilities, nodeprotocol.CapabilitySyncGzip) {
		responseLimit = nodeprotocol.MaxSyncDecodedBytes
	}
	// THE TELEMETRY SUBTREE LEAVES THE CONTROL PATH HERE, before anything reads
	// the report. Everything below this line decides config, roster, directives,
	// tasks and accounting, and none of it may be influenced by a telemetry
	// field — which is what makes "a bad sample cannot cost a node its roster"
	// structural rather than a promise someone has to keep.
	//
	// It is removed rather than validated-and-kept so that no later step can
	// reach it by accident, including the clones that put the report in the
	// latest-full cache.
	host := report.Host
	report.Host = nil
	// THE AUDIT SUBTREE LEAVES THE CONTROL PATH AT THE SAME PLACE, and for the
	// same reason: nothing that decides config, roster or accounting may be
	// influenced by it, and the clones that fill the latest-report cache must not
	// carry up to a MiB of hits per agent. It is handed to the ingest queue only
	// after the response exists (below), so a sync that fails is retried by the
	// node with the same batch and the batch is not seen twice.
	audit := report.Audit
	report.Audit = nil
	if err := nodeprotocol.ValidateNodeReportBase(report); err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: invalid report: %w", err)
	}
	// A node has one ordered sync stream. Serializing by stable agent identity
	// keeps complete -> applied observation -> mint -> offer in that order even
	// when an HTTP retry overlaps the original request. Different agents remain
	// fully concurrent.
	unlock := s.agentLocks.Lock(report.AgentID)
	defer unlock()
	agent, err := s.agents.GetByAgentID(ctx, report.AgentID)
	if err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: resolve agent: %w", err)
	}
	snapshot, err := s.desired.Load(ctx, agent.PanelID)
	if err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: desired snapshot: %w", err)
	}
	now := s.now().UTC()
	if err := s.ingestReport(ctx, agent, snapshot, report, now); err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	normalized, choices, err := s.recordPanelObservation(ctx, agent, report, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	if normalized {
		// The snapshot was loaded before observations were ingested. A successful
		// normalization changes that authoritative desired state, so reload it
		// before minting this very response; otherwise the same sync round could
		// send the pre-normalization fingerprint back to the node.
		snapshot, err = s.desired.Load(ctx, agent.PanelID)
		if err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: reload normalized desired snapshot: %w", err)
		}
	}

	// THE CONFIGURED CORE IS RE-CHECKED AGAINST THE REVIEW THAT IS CURRENT NOW.
	//
	// It runs HERE, after the report has been ingested, and that placement is the
	// requirement rather than an accident: a node's reports and its task receipts
	// are the record of what it has done, and a stale core selection must not cost
	// the panel that record. Everything below this line is outbound.
	//
	// THE READ IS CACHED. The reader refreshes on its own TTL and de-duplicates
	// concurrent refreshes, so the hot path takes a lock and compares a time; it
	// does not reach the origin.
	coreDocument, err := s.coreCatalog.Document(ctx)
	if err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
	}
	// THE DESTINATION POLICY. The node's status is a CONTROL INPUT, unlike host
	// telemetry, so it is taken before the config is built: a refusal has to
	// change this round's answer (fall back), and the record of what applied
	// has to be current when the compile reads it. Taking it after the
	// response would make "refused, so fall back" arrive a sync late.
	var policyIssues []domain.NodeAgentIssue
	if s.policies != nil {
		issues, err := s.policies.ObserveStatus(ctx, agent.AgentID, report.Capabilities, report.PolicyStatus)
		if err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: observe destination policy status: %w", err)
		}
		policyIssues = append(policyIssues, issues...)
	}
	// MANAGED RELAYS AND TUNNELS. Sent only to a node that declared it can run
	// them: one that cannot would ignore the field, open nothing, and report the
	// config applied. They are worked out before the policy compile, which needs
	// to know which listeners leave through a tunnel, and attached after it, so
	// the policy sees exactly the config it always did.
	var transitBody nodeprotocol.Transit
	var egressExit map[int64]int64
	var linkPlan transit.LinkPlan
	var routeExits map[int64][]destpolicy.RouteExit
	var routeIssues []domain.NodeAgentIssue
	var routeWithheld, late []route.Withheld
	// routedNodes are the nodes the server is configured for, before any is left
	// out of this config: what the routes of each is recorded against.
	routedNodes := snapshot.Nodes
	var forwardMethods map[nodeprotocol.ForwardKey]string
	var frontIssues []domain.NodeAgentIssue
	var frontPlan front.Plan
	if s.transit != nil {
		// LISTENERS WHOSE ROUTES CANNOT BE SENT ARE DECIDED FIRST, before the
		// shared ports and the links are planned from the config: one left out is
		// then simply not there, and drops out of a shared port with its routes
		// instead of being named by a front the server would refuse whole.
		if s.routes != nil {
			early, err := s.routes.Early(ctx, agent.PanelID, report.Capabilities, snapshot.Nodes)
			if err != nil {
				return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
			}
			if len(early.Withheld) > 0 {
				routeWithheld = append(routeWithheld, early.Withheld...)
				routeIssues = append(routeIssues, early.Issues...)
				snapshot = withoutNodes(snapshot, early.Withheld)
			}
		}
		// A forward or an incoming link on a port one of this server's own
		// inbounds uses would make the node refuse the whole config, listener
		// changes included. Such an object is withheld here instead; its
		// save-time check normally stops it, but an inbound can move onto the
		// port afterwards.
		listening := make(map[int]bool, len(snapshot.Nodes))
		for _, node := range snapshot.Nodes {
			if node != nil && node.DesiredPort > 0 {
				listening[node.DesiredPort] = true
			}
		}
		// SNI-SHARED PORTS. A shared port is taken for everything that is not
		// behind its front, the loopback ports of fronted listeners with it, so
		// a forward or a link on one is withheld like any other collision. The
		// forwards a front routes to are the exception: they are bound where the
		// front is, relay TCP only, and are sent with the fronts below.
		if s.fronts != nil {
			plan, err := s.fronts.PlanFor(ctx, agent.PanelID, report.Capabilities, agent.DesiredCoreEngine,
				domain.NodeCoreEngine(report.CoreEngine), presentNodes(snapshot))
			if err != nil {
				return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
			}
			frontPlan = plan
			for _, w := range plan.Withheld {
				metrics.FrontsWithheldTotal.With(w.Reason).Inc()
				frontIssues = append(frontIssues, domain.NodeAgentIssue{
					Code: IssueFrontWithheld, Key: string(nodeprotocol.NewFrontKey(w.FrontID)), Detail: w.Reason + ": " + w.Detail,
				})
			}
			for port := range frontPlan.Ports {
				listening[port] = true
			}
		}
		if slices.Contains(report.Capabilities, nodeprotocol.CapabilityTransitForward) {
			forwards, methods, err := s.transit.ForwardsWithProbeMethods(ctx, agent.PanelID)
			forwardMethods = methods
			if err != nil {
				return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
			}
			for _, forward := range forwards {
				if frontPlan.FrontedForwards[forward.Key] {
					continue
				}
				// Two lines claiming one port (two saves that raced) would fail
				// the same way; the older line keeps the port.
				if listening[forward.Port] {
					log.Warn("managed relay withheld: its entry port is already taken on the entry server",
						"agent_id", agent.AgentID, "forward", string(forward.Key), "port", forward.Port)
					continue
				}
				listening[forward.Port] = true
				transitBody.Forwards = append(transitBody.Forwards, forward)
			}
		}
		if slices.Contains(report.Capabilities, nodeprotocol.CapabilityTransitLink) {
			plan, err := s.transit.LinksForFronts(ctx, agent.PanelID, listening, slices.Contains(report.Capabilities, nodeprotocol.CapabilityTransitRoute), frontPlan.LinksIn)
			if err != nil {
				return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
			}
			linkPlan = plan
			transitBody.LinksIn, transitBody.LinksOut, transitBody.Egress = plan.In, plan.Out, plan.Egress
			egressExit = plan.EgressExit
			if plan.Changed && s.invalidateRender != nil {
				// What is sent decides which bound nodes may be published.
				s.invalidateRender()
			}
		}
		// ROUTES BY DESTINATION go only to an agent that declared transit.route.v1:
		// one that did not would ignore them and send every destination through
		// the default exit with no error anywhere. A listener whose routes cannot
		// be sent is left out of the config instead (below), so its destinations
		// never go through the default exit by omission.
		if s.routes != nil {
			routePlan, err := s.routes.PlanFor(ctx, route.PlanInput{
				PanelID: agent.PanelID, Capabilities: report.Capabilities, Nodes: snapshot.Nodes,
				RouteOut: linkPlan.RouteOut, RouteKeys: linkPlan.RouteKeys, Out: linkPlan.Out,
				Egress: linkPlan.Egress, EgressExit: linkPlan.EgressExit,
			})
			if err != nil {
				return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
			}
			transitBody.Routes, routeExits = routePlan.Routes, routePlan.Exits
			routeIssues = append(routeIssues, routePlan.Issues...)
			late = routePlan.Withheld
			routeWithheld = append(routeWithheld, late...)
		}
		frontPlan = frontPlan.WithServedLinks(transitBody.LinksIn)
		if len(frontPlan.Fronts) > 0 {
			transitBody.Forwards = append(transitBody.Forwards, frontPlan.Forwards...)
			sort.Slice(transitBody.Forwards, func(i, j int) bool { return transitBody.Forwards[i].Key < transitBody.Forwards[j].Key })
			transitBody.Fronts = frontPlan.Fronts
			// What is sent must be what the contract accepts: fronts it would
			// refuse fail the whole config, so they are withheld instead (the
			// listeners keep their public ports), recorded as an issue and
			// counted. The fronts are checked on their own first, so a fault
			// of some other object (a link) is not charged to them, and a
			// fault that is theirs is not hidden by it.
			if faulty := frontContractFaults(transitBody, frontPlan); len(faulty) > 0 {
				drop := map[nodeprotocol.FrontKey]bool{}
				for key, reason := range faulty {
					drop[key] = true
					log.Warn("SNI front withheld: the contract refuses it", "agent_id", agent.AgentID, "front", string(key), "err", reason)
					metrics.FrontsWithheldTotal.With(front.WithheldContract).Inc()
					frontIssues = append(frontIssues, domain.NodeAgentIssue{
						Code: IssueFrontWithheld, Key: string(key), Detail: front.WithheldContract + ": " + reason,
					})
				}
				kept := transitBody.Forwards[:0]
				for _, forward := range transitBody.Forwards {
					if !frontPlan.FrontedForwards[forward.Key] {
						kept = append(kept, forward)
					}
				}
				oldLinks := frontPlan.LinksIn
				frontPlan = frontPlan.Without(drop)
				var droppedLinks []nodeprotocol.LinkKey
				for _, in := range oldLinks {
					if !slices.ContainsFunc(frontPlan.LinksIn, func(kept nodeprotocol.LinkIn) bool { return kept.Key == in.Key }) {
						droppedLinks = append(droppedLinks, in.Key)
					}
				}
				if len(droppedLinks) > 0 {
					transitBody.LinksIn = slices.DeleteFunc(transitBody.LinksIn, func(in nodeprotocol.LinkIn) bool {
						return slices.Contains(droppedLinks, in.Key)
					})
					if err := s.transit.WithholdIncomingLinks(ctx, droppedLinks); err != nil {
						return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
					}
					if s.invalidateRender != nil {
						s.invalidateRender()
					}
				}
				transitBody.Forwards = append(kept, frontPlan.Forwards...)
				sort.Slice(transitBody.Forwards, func(i, j int) bool { return transitBody.Forwards[i].Key < transitBody.Forwards[j].Key })
				transitBody.Fronts = frontPlan.Fronts
			}
			// A forward behind a front is probed by a TLS handshake naming the
			// route's server name: a TCP connect would only show the front up.
			if len(frontPlan.FrontedForwards) > 0 {
				if forwardMethods == nil {
					forwardMethods = map[nodeprotocol.ForwardKey]string{}
				}
				for key := range frontPlan.FrontedForwards {
					forwardMethods[key] = linkprobe.Method(false, true)
				}
			}
		}
		// The probe block comes last: it names what the config above holds.
		// Only an agent that declared transit.probe.v1 gets it, so everyone
		// else receives the bytes they always did.
		if s.probes != nil {
			probe, err := s.probes.Config(ctx, report.Capabilities, &transitBody, forwardMethods)
			if err != nil {
				return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
			}
			transitBody.Probe = probe
		}
	}
	// A front that is not being sent cannot give its listeners their public port
	// when two or more would bind the same one: Node refuses a config where that
	// happens, so those listeners are left out of this one (as Node itself does
	// when it drops a front it cannot run) and an issue names them. A front with a
	// single listener lets it fall back to its public port.
	if s.fronts != nil {
		unserved, err := s.fronts.UnservedListeners(ctx, agent.PanelID, frontPlan, presentNodes(snapshot))
		if err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
		}
		if len(unserved) > 0 {
			drop := map[int64]bool{}
			for _, u := range unserved {
				drop[u.NodeID] = true
				log.Warn("listener withheld: its shared port is not being served and another listener would bind the same port",
					"agent_id", agent.AgentID, "node_id", u.NodeID, "port", u.Port)
				metrics.FrontsWithheldTotal.With("listener_unserved").Inc()
				frontIssues = append(frontIssues, domain.NodeAgentIssue{
					Code: IssueFrontWithheld, Key: string(nodeprotocol.NewListenerKey(u.NodeID)),
					Detail: fmt.Sprintf("listener_unserved: %s is not sent because its shared port %d is not being served and it would share that port with %s",
						u.Name, u.Port, strings.Join(u.With, ", ")),
				})
			}
			clone := *snapshot
			clone.Nodes = nil
			for _, node := range snapshot.Nodes {
				if node != nil && !drop[node.ID] {
					clone.Nodes = append(clone.Nodes, node)
				}
			}
			snapshot = &clone
		}
	}
	// A listener whose routes cannot be sent is not sent either: its routed
	// destinations would otherwise leave through its default exit.
	for _, w := range routeWithheld {
		log.Warn("listener withheld: its routes cannot be sent", "agent_id", agent.AgentID, "node_id", w.NodeID, "reason", w.Reason, "detail", w.Detail)
	}
	if len(late) > 0 {
		snapshot = withoutNodes(snapshot, late)
	}
	// Routes name listeners of this config only: one a front left out is not sent.
	if len(transitBody.Routes) > 0 {
		present := make(map[nodeprotocol.ListenerKey]bool, len(snapshot.Nodes))
		for _, node := range snapshot.Nodes {
			if node != nil {
				present[nodeprotocol.NewListenerKey(node.ID)] = true
			}
		}
		kept := transitBody.Routes[:0:0]
		for _, lr := range transitBody.Routes {
			if present[lr.Listener] {
				kept = append(kept, lr)
			} else if id, err := lr.Listener.RowID(); err == nil {
				delete(routeExits, id)
			}
		}
		transitBody.Routes = kept
	}
	if s.routes != nil {
		reasons := make(map[int64]string, len(routeWithheld))
		for _, w := range routeWithheld {
			reasons[w.NodeID] = w.Reason
		}
		moved, err := s.routes.RecordSent(ctx, routedNodes, transitBody.Routes, reasons)
		if err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
		}
		if moved && s.invalidateRender != nil {
			// A node that is withheld is not published, and one that is sent again is.
			s.invalidateRender()
		}
	}
	configBody, err := buildConfig(snapshot, agent, coreDocument)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	policyDigest := ""
	if s.policies != nil {
		// A compile that cannot decide fails the sync, and the node keeps what it
		// has: sending the config without a policy would silently stop
		// enforcing one the node is running.
		result, err := s.policies.Compile(ctx, destpolicy.CompileInput{
			PanelID: agent.PanelID, AgentID: agent.AgentID, Capabilities: report.Capabilities,
			Snapshot: snapshot, Config: configBody, CoreVersion: report.CoreVersion, AgentVersion: report.AgentVersion,
			AuditCollect: choices.collect, DestResolve: choices.resolve, Egress: egressExit, RouteExits: routeExits,
		})
		if err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: compile destination policy: %w", err)
		}
		configBody.Policy, policyDigest = result.Policy, result.Digest
		policyIssues = append(policyIssues, result.Issues...)
	}
	// SOURCE GUARD (ADR 0044). The sets go in the config and each guarded client
	// refers to one from the roster, only for an agent that declared
	// transit.source_guard.v1: one that did not would ignore both and accept
	// every connection while the config reads as applied. The sets are worked
	// out from the nodes of THIS config, so a listener that was left out above
	// has no set and none of its clients a guard.
	var guardPlan sourceguard.Plan
	if s.guard != nil {
		guardPlan, err = s.guard.PlanFor(ctx, report.Capabilities, snapshot.Nodes)
		if err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
		}
		transitBody.SourceSets = guardPlan.Sets
		withheld, err := s.guard.WithheldLines(ctx, report.Capabilities, snapshot.Nodes)
		if err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: %w", err)
		}
		policyIssues = append(policyIssues, sourceGuardIssues(withheld)...)
	}
	if len(transitBody.Forwards) > 0 || len(transitBody.LinksIn) > 0 || len(transitBody.LinksOut) > 0 || len(transitBody.Fronts) > 0 || len(transitBody.Routes) > 0 || len(transitBody.SourceSets) > 0 {
		configBody.Transit = &transitBody
	}
	policyIssues = append(policyIssues, frontIssues...)
	policyIssues = append(policyIssues, routeIssues...)
	policyIssues = append(policyIssues, s.orphanTunnelIssues(ctx, snapshot)...)
	policyIssues = append(policyIssues, cdnUnfitIssues(snapshot)...)
	policyIssues = append(policyIssues, rosterCapacityIssues(snapshot)...)
	if len(policyIssues) > 0 {
		for i := range policyIssues {
			policyIssues[i].AgentID = agent.AgentID
		}
		if err := s.issues.RecordBatch(ctx, agent.AgentID, policyIssues, now); err != nil {
			return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: record destination policy issues: %w", err)
		}
	}
	configStream, err := s.mintConfig(ctx, agent, configBody, policyDigest, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	rosterBody := buildRoster(snapshot, nodeprotocol.Version{Epoch: agent.Epoch, Version: configStream.DesiredVersion}, guardPlan)
	rosterStream, err := s.mint(ctx, agent, domain.NodeAgentStreamRoster, rosterBody, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	directivesBody, envelope, err := s.buildDirectives(ctx, agent, snapshot, report,
		nodeprotocol.Version{Epoch: agent.Epoch, Version: rosterStream.DesiredVersion}, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	directivesStream, err := s.mint(ctx, agent, domain.NodeAgentStreamDirectives, directivesBody, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	response := nodeprotocol.SyncResponse{
		Envelope:   envelope,
		Config:     segmentFor(report.Have[nodeprotocol.StreamConfig], agent.Epoch, configStream, configBody),
		Roster:     segmentFor(report.Have[nodeprotocol.StreamRoster], agent.Epoch, rosterStream, rosterBody),
		Directives: segmentFor(report.Have[nodeprotocol.StreamDirectives], agent.Epoch, directivesStream, directivesBody),
	}
	taskJSONBudget, err := availableTaskJSONBytes(response, responseLimit)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	tasks, hadFirstOffer, err := s.offerTasks(ctx, agent.AgentID, report.Capabilities, taskJSONBudget, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	response.Tasks = tasks
	if hadFirstOffer {
		response.Envelope.NextPollSeconds = 1
	}

	// TELEMETRY RUNS LAST, after the control response is fully built, so a slow
	// or failing store can delay this round but never change it. The ingest
	// returns no error by construction: the only way this feature could break the
	// data plane is by returning one, and that is the outcome it exists to be
	// incapable of.
	if s.host != nil {
		if host != nil {
			s.host.Ingest(ctx, agent.AgentID, host, now)
		}
		// A refresh the operator asked for is answered by the sample just stored.
		// Asking after the ingest is what lets the window close on this round
		// rather than waiting for a timer, and a failed write leaves it open so
		// the next round tries again.
		response.Envelope.WantHostReport = s.host.WantsHostReport(agent.AgentID)
	}

	if err := nodeprotocol.ValidateSyncResponse(response); err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: invalid response: %w", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: encode response: %w", err)
	}
	if int64(len(encoded)) > responseLimit {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: response exceeds %d bytes", responseLimit)
	}
	// The response is complete and will be sent: only now does the node's batch
	// leave for the store. Enqueue does no database work and cannot fail the sync.
	if s.audit != nil && audit != nil {
		s.audit.Enqueue(agent.AgentID, agent.PanelID, report.Capabilities, audit)
	}
	return response, nil
}

// panelChoices are the destination-policy choices read from the panel row a
// sync already loads: how much the node records, and how its routing treats a
// hostname against IP rules.
type panelChoices struct {
	collect domain.AuditCollect
	resolve domain.DestResolve
}

// recordPanelObservation records what the node reports about itself on its
// panel row, and returns whether a REALITY fingerprint was normalized and the
// panel's destination-policy choices, read from the very row it loads (the
// compile needs them and this is the one panel read of a sync).
func (s *Service) recordPanelObservation(ctx context.Context, agent *domain.NodeAgent, report nodeprotocol.NodeReport, now time.Time) (bool, panelChoices, error) {
	if s.panels == nil || agent == nil {
		return false, panelChoices{}, nil
	}
	panel, err := s.panels.GetByID(ctx, agent.PanelID)
	if err != nil {
		return false, panelChoices{}, fmt.Errorf("nodesync: load native panel observation target: %w", err)
	}
	choices := panelChoices{collect: panel.AuditCollect, resolve: panel.DestResolve}
	panelVersion := report.AgentVersion
	if panelVersion == "" {
		panelVersion = panel.PanelVersion
	}
	coreVersion := report.CoreVersion
	if coreVersion == "" {
		coreVersion = panel.XrayVersion
	}
	observedEngine := domain.NodeCoreEngine(report.CoreEngine)
	engineChanged := observedEngine != "" && observedEngine != agent.ObservedCoreEngine
	if engineChanged && s.agents != nil {
		if err := s.agents.UpdateCoreObservation(ctx, agent.AgentID, observedEngine); err != nil {
			return false, panelChoices{}, fmt.Errorf("nodesync: persist native core engine observation: %w", err)
		}
	}
	if err := s.panels.UpdateVersion(ctx, agent.PanelID, panelVersion, coreVersion, &now); err != nil {
		return false, panelChoices{}, fmt.Errorf("nodesync: persist native panel observation: %w", err)
	}
	engine := observedEngine
	if engine == "" {
		engine = agent.ObservedCoreEngine
	}
	normalized := false
	if engine == domain.NodeCoreXray && s.normalizeRealityFingerprints != nil {
		changed, err := s.normalizeRealityFingerprints(ctx, agent.PanelID, coreVersion)
		if err != nil {
			return false, panelChoices{}, fmt.Errorf("nodesync: normalize REALITY fingerprints: %w", err)
		}
		normalized = changed > 0
	}
	if (coreVersion != panel.XrayVersion || engineChanged) && s.invalidateRender != nil {
		s.invalidateRender()
	}
	return normalized, choices, nil
}

func (s *Service) mint(ctx context.Context, agent *domain.NodeAgent, stream domain.NodeAgentStreamName, body any, now time.Time) (*domain.NodeAgentStream, error) {
	canonical, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("nodesync: encode %s: %w", stream, err)
	}
	stored, _, err := s.agents.MintStream(ctx, agent.AgentID, stream, canonical, now)
	if err != nil {
		return nil, fmt.Errorf("nodesync: mint %s: %w", stream, err)
	}
	return stored, nil
}

// mintConfig mints the config stream. Without a policy it is exactly mint: the
// body is encoded as it always was, which is what keeps a policy-less config
// byte-identical to before. With one, the canonical bytes are cached under the
// digest of the part that is cheap to encode (the config without its policy)
// and the policy's own digest, so an idle sync re-encodes nothing and an
// unchanged policy cannot move the ETag.
func (s *Service) mintConfig(ctx context.Context, agent *domain.NodeAgent, body nodeprotocol.ConfigBody, policyDigest string, now time.Time) (*domain.NodeAgentStream, error) {
	if body.Policy == nil {
		s.mu.Lock()
		delete(s.configCache, agent.AgentID)
		s.mu.Unlock()
		return s.mint(ctx, agent, domain.NodeAgentStreamConfig, body, now)
	}
	bare := body
	bare.Policy = nil
	bareBytes, err := json.Marshal(bare)
	if err != nil {
		return nil, fmt.Errorf("nodesync: encode %s: %w", domain.NodeAgentStreamConfig, err)
	}
	sum := sha256.Sum256(bareBytes)
	key := hex.EncodeToString(sum[:]) + ":" + policyDigest

	s.mu.RLock()
	cached, hit := s.configCache[agent.AgentID]
	s.mu.RUnlock()
	canonical := cached.canonical
	if !hit || cached.key != key || policyDigest == "" {
		if canonical, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("nodesync: encode %s: %w", domain.NodeAgentStreamConfig, err)
		}
		if policyDigest != "" {
			s.mu.Lock()
			if s.configCache == nil {
				s.configCache = map[string]configCacheEntry{}
			}
			s.configCache[agent.AgentID] = configCacheEntry{key: key, canonical: canonical}
			s.mu.Unlock()
		}
	}
	stored, _, err := s.agents.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig, canonical, now)
	if err != nil {
		return nil, fmt.Errorf("nodesync: mint %s: %w", domain.NodeAgentStreamConfig, err)
	}
	return stored, nil
}

type listenerConfig = domain.NodeConfigIntent

// errSelectionWithdrawn means the release this node is configured for is no longer
// one the review offers, or no longer one whose restriction acknowledgement matches.
var errSelectionWithdrawn = errors.New("nodesync: the configured core release is no longer an offer the review supports")

// buildConfig describes the listener set and the core selection this agent is
// configured for, RE-CHECKED AGAINST THE REVIEW THAT IS CURRENT NOW.
//
// WHAT THIS IS: the panel stops dispatching a core release the review has since
// withdrawn, and a release whose restriction acknowledgement no longer matches what
// the node is configured with. The check reads the reviewed catalog through its
// port — the same review the selector offers from and the node runtime reads — so
// a release pulled for a failed handshake stops being pushed to the fleet without
// waiting for a panel release.
//
// WHAT THIS IS NOT, AND THE DISTINCTION MATTERS: it is NOT revocation. A node that
// already received and applied the selection keeps running it. No configuration is
// withdrawn from a running node, nothing is uninstalled, and a node that is offline
// while the withdrawal happens will keep its core until it next syncs and is
// refused. This stops the choice from spreading and stops it from being re-sent; it
// does not take it back. Calling it a runtime revocation mechanism would promise
// something the panel cannot do.
//
// WHY IT IS NOT MORE THAN THAT. The protocol has no "there is no config for you"
// answer that is valid for an agent with no local bytes — an unchanged segment is
// rejected by the agent when its etag differs from what it applied, and the zero
// core selection means "use the recommended release from YOUR catalog", which would
// silently move a node to a different core rather than decline to move it. So the
// refusal is a failed sync: the node keeps what it has, the operator sees why, and
// nothing is dispatched in the meantime.
func buildConfig(snapshot *ports.NativeDesiredSnapshot, agent *domain.NodeAgent, catalog ports.CoreCatalogDocument) (nodeprotocol.ConfigBody, error) {
	engine := domain.NodeCoreXray
	version := ""
	allowRestricted := false
	if agent != nil {
		engine = domain.NormalizeNodeCoreEngine(agent.DesiredCoreEngine)
		version = strings.TrimSpace(agent.DesiredCoreVersion)
		allowRestricted = agent.AllowRestrictedReality
	}
	if !engine.Valid() {
		return nodeprotocol.ConfigBody{}, fmt.Errorf("nodesync: desired core engine %q is unsupported", engine)
	}
	normalized, err := domain.NormalizeCoreVersion(version)
	if err != nil {
		return nodeprotocol.ConfigBody{}, fmt.Errorf("nodesync: no canonical desired core release is configured for this agent: %w", err)
	}

	// THE REVIEW MAY HAVE MOVED SINCE THE OPERATOR CHOSE. Resolve answers from the
	// selectable set, so a release that has been withdrawn or removed fails here —
	// and a release the review now considers restricted, or no longer considers
	// restricted, fails the comparison below.
	release, err := catalog.Resolve(string(engine), normalized)
	if err != nil {
		log.Warn("core selection refused: the reviewed catalog no longer offers it",
			"agent_id", agent.AgentID, "engine", string(engine), "version", normalized, "err", err)
		return nodeprotocol.ConfigBody{}, fmt.Errorf("%w: %s %s: %w", errSelectionWithdrawn, engine, normalized, err)
	}
	if release.RequiresConfirmation != allowRestricted {
		log.Warn("core selection refused: the acknowledgement no longer matches the review",
			"agent_id", agent.AgentID, "engine", string(engine), "version", normalized,
			"tier", release.Tier, "requires_confirmation", release.RequiresConfirmation, "configured", allowRestricted)
		return nodeprotocol.ConfigBody{}, fmt.Errorf("%w: %s %s now requires confirmation=%t and this node is configured with %t",
			errSelectionWithdrawn, engine, normalized, release.RequiresConfirmation, allowRestricted)
	}

	body := nodeprotocol.ConfigBody{
		Listeners: make([]nodeprotocol.Listener, 0, len(snapshot.Nodes)),
		Core: nodeprotocol.CoreSelection{
			Engine: string(engine), Version: normalized, AllowRestrictedReality: allowRestricted,
		},
	}
	for _, node := range snapshot.Nodes {
		if node == nil {
			continue
		}
		raw, err := json.Marshal(node.ConfigIntent())
		if err != nil {
			return nodeprotocol.ConfigBody{}, fmt.Errorf("nodesync: encode listener %d: %w", node.ID, err)
		}
		body.Listeners = append(body.Listeners, nodeprotocol.Listener{
			Key: nodeprotocol.NewListenerKey(node.ID), Config: nodeprotocol.RawConfig(raw),
		})
	}
	body.Coverage = nodeprotocol.SegmentCounts{Entries: len(body.Listeners)}
	return body, nil
}

// buildRoster lists the clients of the snapshot. guard is the source guard of the
// landing (empty when it has none, or cannot enforce one): a roster built
// without it is what it was before source guards existed.
func buildRoster(snapshot *ports.NativeDesiredSnapshot, configVersion nodeprotocol.Version, guard sourceguard.Plan) nodeprotocol.RosterBody {
	nodeIDs := make(map[int64]struct{}, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node != nil {
			nodeIDs[node.ID] = struct{}{}
		}
	}
	body := nodeprotocol.RosterBody{
		Clients:          make([]nodeprotocol.Client, 0, len(snapshot.Clients)),
		MinConfigVersion: configVersion,
	}
	subjects := make(map[int64]struct{})
	for _, desired := range snapshot.Clients {
		client := desired.Client
		if client == nil {
			continue
		}
		listeners := make([]nodeprotocol.ListenerKey, 0, len(desired.Inbounds))
		flow := ""
		for _, attachment := range desired.Inbounds {
			if _, exists := nodeIDs[attachment.NodeID]; exists {
				listeners = append(listeners, nodeprotocol.NewListenerKey(attachment.NodeID))
				if attachment.FlowOverride != "" {
					if flow != "" && flow != attachment.FlowOverride {
						// clientplan guarantees one flow class per client row. Keep
						// this defensive branch deterministic; the node compiler will
						// reject a malformed plan instead of receiving an arbitrary
						// last attachment's value.
						flow = "__conflict__"
					} else {
						flow = attachment.FlowOverride
					}
				}
			}
		}
		sort.Slice(listeners, func(i, j int) bool { return listeners[i] < listeners[j] })
		body.Clients = append(body.Clients, nodeprotocol.Client{
			Key: nodeprotocol.NewClientKey(client.ID), Subject: nodeprotocol.NewSubjectKey(client.UserID),
			Listeners: listeners, Enabled: client.DesiredEnable,
			ExpiresAtMS: client.DesiredExpiryTime,
			Credentials: nodeprotocol.Credential{
				Username: client.Email, UUID: client.UUID, Password: client.Password, Flow: flow,
			},
			SourceGuard: guard.Guard(client, len(listeners)),
		})
		subjects[client.UserID] = struct{}{}
	}
	body.Coverage = nodeprotocol.SegmentCounts{Entries: len(body.Clients), Subjects: len(subjects)}
	return body
}

func segmentFor[T any](have nodeprotocol.StreamState, epoch uint64, stream *domain.NodeAgentStream, body T) nodeprotocol.Segment[T] {
	result := nodeprotocol.Segment[T]{
		Version: nodeprotocol.Version{Epoch: epoch, Version: stream.DesiredVersion},
		ETag:    nodeprotocol.ETag(stream.DesiredETag),
	}
	if have.Applied.Epoch == epoch && string(have.ETag) == stream.DesiredETag && stream.DesiredETag != "" {
		result.Unchanged = true
		return result
	}
	result.Body = &body
	return result
}

func (s *Service) ingestReport(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, report nodeprotocol.NodeReport, now time.Time) error {
	// CompleteBatch is atomic across every result in this report. The complete
	// report is intentionally not one giant transaction: if a later issue,
	// stream, or observation write fails, the agent retains its outbox because
	// HTTP returns non-2xx and the immutable batch replays against its durable
	// terminal records or quarantined evidence. A receipt does not imply that
	// quarantined evidence is an authoritative task outcome.
	if err := nodeprotocol.ValidateTaskResults(report.TaskResults); err != nil {
		return fmt.Errorf("nodesync: invalid task results: %w", err)
	}
	// Capabilities are a current fact, not a sticky promise. Persist every
	// authenticated report, including an empty set, so disabling or removing an
	// upgrade helper immediately revokes future task admission.
	if err := s.agents.UpdateProtocolObservation(ctx, agent.AgentID, report.ProtocolVersion, report.Capabilities, now); err != nil {
		return fmt.Errorf("nodesync: record protocol observation: %w", err)
	}
	// What the source guard publishes depends on the server's addresses and on
	// whether its agent can guard, so a change of either refreshes subscriptions.
	guardMoved := slices.Contains(agent.ObservedCapabilities, nodeprotocol.CapabilitySourceGuard) !=
		slices.Contains(report.Capabilities, nodeprotocol.CapabilitySourceGuard)
	if recorder, ok := s.agents.(interface {
		RecordObservedAddress(ctx context.Context, agentID, address string) error
	}); ok {
		if address := sourceaddr.From(ctx); address != "" && address != agent.ObservedAddress {
			if err := recorder.RecordObservedAddress(ctx, agent.AgentID, address); err != nil {
				return fmt.Errorf("nodesync: record server address: %w", err)
			}
			guardMoved = true
		}
	}
	// The server's own public addresses are what a landing accepts a managed
	// relay line's traffic from (ADR 0044). Only a full report from an agent that
	// declared the capability carries them; a partial one says nothing, and an
	// empty full list is a fact (no public address), not an absence.
	if !report.Partial && slices.Contains(report.Capabilities, nodeprotocol.CapabilitySourceGuard) &&
		!slices.Equal(agent.InterfaceAddresses, report.InterfaceAddresses) {
		if recorder, ok := s.agents.(interface {
			RecordInterfaceAddresses(ctx context.Context, agentID string, addresses []string) error
		}); ok {
			if err := recorder.RecordInterfaceAddresses(ctx, agent.AgentID, report.InterfaceAddresses); err != nil {
				return fmt.Errorf("nodesync: record interface addresses: %w", err)
			}
			guardMoved = true
		}
	}
	if guardMoved && s.invalidateRender != nil {
		s.invalidateRender()
	}
	if s.allowlistResync != nil && allowlistCapabilitiesChanged(agent.ObservedCapabilities, report.Capabilities) {
		// A node that newly declares the capabilities (a fresh install, an
		// upgrade) must get its allowlist members' clients, and one that lost
		// them must lose them: nothing else would notice.
		s.allowlistResync(ctx, agent.PanelID)
	}
	results := make([]domain.NodeAgentTaskResult, len(report.TaskResults))
	for i := range report.TaskResults {
		results[i] = domain.NodeAgentTaskResult{
			TaskID: report.TaskResults[i].ID, Kind: report.TaskResults[i].Kind,
			InputSHA256: report.TaskResults[i].InputSHA256, OK: report.TaskResults[i].OK,
			NotAfterMS:    report.TaskResults[i].NotAfterMS,
			Indeterminate: report.TaskResults[i].Indeterminate,
			Result:        append([]byte(nil), report.TaskResults[i].Result...),
			ErrorCode:     report.TaskResults[i].ErrorCode, Error: report.TaskResults[i].Error,
		}
	}
	if err := s.tasks.CompleteBatch(ctx, agent.AgentID, results, now); err != nil {
		return fmt.Errorf("nodesync: complete agent tasks: %w", err)
	}
	issues := make([]domain.NodeAgentIssue, len(report.Issues))
	for i := range report.Issues {
		issues[i] = domain.NodeAgentIssue{
			AgentID: agent.AgentID, Code: report.Issues[i].Code,
			Key: report.Issues[i].Key, Detail: report.Issues[i].Detail,
		}
	}
	if err := s.issues.RecordBatch(ctx, agent.AgentID, issues, now); err != nil {
		return fmt.Errorf("nodesync: record agent issues: %w", err)
	}
	streams := make(map[string]*domain.NodeAgentStream, 3)
	for _, item := range []struct {
		wire string
		db   domain.NodeAgentStreamName
	}{
		{nodeprotocol.StreamConfig, domain.NodeAgentStreamConfig},
		{nodeprotocol.StreamRoster, domain.NodeAgentStreamRoster},
		{nodeprotocol.StreamDirectives, domain.NodeAgentStreamDirectives},
	} {
		stream, err := s.agents.GetStream(ctx, agent.AgentID, item.db)
		if err != nil {
			return fmt.Errorf("nodesync: load %s state: %w", item.wire, err)
		}
		streams[item.wire] = stream
		have := report.Have[item.wire]
		if err := s.agents.RecordApplied(ctx, agent.AgentID, item.db,
			have.Applied.Epoch, have.Applied.Version, string(have.ETag), now); err != nil {
			return fmt.Errorf("nodesync: record %s applied: %w", item.wire, err)
		}
	}
	if report.Partial {
		return nil
	}
	if err := s.recordSourceGuardHits(ctx, agent, snapshot, report, now); err != nil {
		return err
	}
	s.mu.Lock()
	s.reports[agent.AgentID] = receivedFullReport{
		report: cloneObservationReport(report), receivedAtMS: now.UnixMilli(),
	}
	s.mu.Unlock()
	return s.reconcileObjects(ctx, agent, snapshot, report, streams, now)
}

func (s *Service) reconcileObjects(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, report nodeprotocol.NodeReport, streams map[string]*domain.NodeAgentStream, now time.Time) error {
	missing := make([]domain.NodeAgentIssue, 0)
	renderCredentialsChanged := false
	rosterHave := report.Have[nodeprotocol.StreamRoster]
	rosterCurrent := streams[nodeprotocol.StreamRoster]
	if rosterCurrent != nil && rosterHave.Applied.Epoch == agent.Epoch && string(rosterHave.ETag) == rosterCurrent.DesiredETag {
		var appliedRoster nodeprotocol.RosterBody
		if err := json.Unmarshal(rosterCurrent.DesiredBody, &appliedRoster); err != nil {
			return fmt.Errorf("nodesync: decode applied roster: %w", err)
		}
		appliedClients := make(map[nodeprotocol.ClientKey]nodeprotocol.Client, len(appliedRoster.Clients))
		for _, client := range appliedRoster.Clients {
			appliedClients[client.Key] = client
		}
		// The guard is left out of this comparison on purpose: it restricts where an
		// identity may connect from, not what the identity is, so it does not decide
		// whether the credentials a client holds are the applied ones.
		currentRoster := buildRoster(snapshot, appliedRoster.MinConfigVersion, sourceguard.Plan{})
		statuses := objectStatuses(report.Objects, nodeprotocol.StreamRoster)
		for _, currentClient := range currentRoster.Clients {
			appliedClient, existed := appliedClients[currentClient.Key]
			if !existed || !sameProtocolClient(appliedClient, currentClient) {
				continue
			}
			clientID, parseErr := currentClient.Key.RowID()
			if parseErr != nil {
				continue
			}
			status, exists := statuses[string(currentClient.Key)]
			state := domain.ClientApplyPending
			var failedAt *time.Time
			if exists {
				state = clientApplyState(status.State)
				if status.FirstFailedAtMS > 0 {
					v := time.UnixMilli(status.FirstFailedAtMS).UTC()
					failedAt = &v
				}
			} else {
				missing = append(missing, domain.NodeAgentIssue{
					AgentID: agent.AgentID, Code: nodeprotocol.IssueReportMissingObject,
					Key:    string(currentClient.Key),
					Detail: "roster object missing from full report",
				})
			}
			var desired *ports.NativeDesiredClient
			for i := range snapshot.Clients {
				if snapshot.Clients[i].Client != nil && snapshot.Clients[i].Client.ID == clientID {
					desired = &snapshot.Clients[i]
					break
				}
			}
			if desired == nil {
				continue
			}
			for _, attachment := range desired.Inbounds {
				update := domain.PSPClientInbound{
					ClientID: clientID, NodeID: attachment.NodeID,
					State: state, AppliedVersion: rosterHave.Applied.Version, FirstFailedAt: failedAt,
				}
				if state == domain.ClientApplyApplied {
					update.AppliedEmail = appliedClient.Credentials.Username
					update.AppliedUUID = appliedClient.Credentials.UUID
					update.AppliedPassword = appliedClient.Credentials.Password
					if attachment.AppliedEmail != update.AppliedEmail ||
						attachment.AppliedUUID != update.AppliedUUID ||
						attachment.AppliedPassword != update.AppliedPassword {
						renderCredentialsChanged = true
					}
				}
				if err := s.clients.UpdateInboundState(ctx, update); err != nil {
					return fmt.Errorf("nodesync: update client %d attachment %d: %w", clientID, attachment.NodeID, err)
				}
			}
		}
	}
	configHave := report.Have[nodeprotocol.StreamConfig]
	configCurrent := streams[nodeprotocol.StreamConfig]
	if configCurrent != nil && configHave.Applied.Epoch == agent.Epoch && string(configHave.ETag) == configCurrent.DesiredETag {
		var appliedConfig nodeprotocol.ConfigBody
		if err := json.Unmarshal(configCurrent.DesiredBody, &appliedConfig); err != nil {
			return fmt.Errorf("nodesync: decode applied config: %w", err)
		}
		appliedListeners := make(map[nodeprotocol.ListenerKey]listenerConfig, len(appliedConfig.Listeners))
		for _, listener := range appliedConfig.Listeners {
			var config listenerConfig
			if err := json.Unmarshal(listener.Config, &config); err != nil {
				return fmt.Errorf("nodesync: decode applied listener %s: %w", listener.Key, err)
			}
			appliedListeners[listener.Key] = config
		}
		statuses := objectStatuses(report.Objects, nodeprotocol.StreamConfig)
		for _, node := range snapshot.Nodes {
			if node == nil {
				continue
			}
			status, exists := statuses[string(nodeprotocol.NewListenerKey(node.ID))]
			applied, existed := appliedListeners[nodeprotocol.NewListenerKey(node.ID)]
			if existed && !exists {
				missing = append(missing, domain.NodeAgentIssue{
					AgentID: agent.AgentID, Code: nodeprotocol.IssueReportMissingObject,
					Key:    string(nodeprotocol.NewListenerKey(node.ID)),
					Detail: "config object missing from full report",
				})
			}
			if !exists || !existed || status.State != nodeprotocol.ObjectApplied || !sameListenerIntent(applied, node) {
				continue
			}
			// An asynchronous push stays pending until this full, current applied
			// receipt arrives. Compare again under the repository's row lock: an
			// admin edit can occur after the desired snapshot above was loaded.
			// Neither a heartbeat nor an old receipt may clear that newer intent.
			changed, err := s.nodes.ConfirmAppliedConfig(ctx, node.ID, agent.PanelID, applied)
			if err != nil {
				return fmt.Errorf("nodesync: confirm listener %d applied config: %w", node.ID, err)
			}
			// Render gates local snapshots on convergence. Refresh it on the
			// transition, not on every periodic replay of an applied receipt.
			if changed {
				renderCredentialsChanged = true
			}
		}
	}
	if s.transit != nil && configCurrent != nil && len(configCurrent.DesiredBody) > 0 {
		var sent nodeprotocol.ConfigBody
		if err := json.Unmarshal(configCurrent.DesiredBody, &sent); err != nil {
			return fmt.Errorf("nodesync: decode sent config: %w", err)
		}
		// Counters are booked against the config the panel sent, which decides
		// which forward keys this node may report at all.
		if err := s.transit.RecordCounters(ctx, sent.Transit, report.ForwardCounters); err != nil {
			return fmt.Errorf("nodesync: %w", err)
		}
		if err := s.transit.RecordLinkCounters(ctx, sent.Transit, report.LinkCounters); err != nil {
			return fmt.Errorf("nodesync: %w", err)
		}
		// Probe results are booked against the same sent config: only its own
		// links and forwards, on the side that probes them.
		if s.probes != nil {
			moved, err := s.probes.Ingest(ctx, agent.PanelID, report.Capabilities, sent.Transit, report.Probes, report.ReportedAtMS)
			if err != nil {
				return fmt.Errorf("nodesync: %w", err)
			}
			renderCredentialsChanged = renderCredentialsChanged || moved
		}
		// Confirmations need the node to hold that config: only then do its
		// object statuses describe these forwards.
		if configHave.Applied.Epoch == agent.Epoch && string(configHave.ETag) == configCurrent.DesiredETag {
			statuses := objectStatuses(report.Objects, nodeprotocol.StreamConfig)
			changed, err := s.transit.Confirm(ctx, agent.PanelID, sent.Transit, statuses, now)
			if err != nil {
				return fmt.Errorf("nodesync: %w", err)
			}
			linksChanged, err := s.transit.ConfirmLinks(ctx, sent.Transit, statuses)
			if err != nil {
				return fmt.Errorf("nodesync: %w", err)
			}
			renderCredentialsChanged = renderCredentialsChanged || changed || linksChanged
			if s.routes != nil {
				// The routes of a listener run when the listener is applied; they
				// do not decide whether a node is published, so nothing is
				// refreshed for them.
				if _, err := s.routes.Confirm(ctx, sent.Transit, statuses, now); err != nil {
					return fmt.Errorf("nodesync: %w", err)
				}
			}
		}
	}
	if renderCredentialsChanged && s.invalidateRender != nil {
		s.invalidateRender()
	}
	if err := s.issues.RecordBatch(ctx, agent.AgentID, missing, now); err != nil {
		return fmt.Errorf("nodesync: record missing report objects: %w", err)
	}
	return nil
}

func sameProtocolClient(a, b nodeprotocol.Client) bool {
	if a.Key != b.Key || a.Subject != b.Subject || a.Enabled != b.Enabled ||
		a.ExpiresAtMS != b.ExpiresAtMS || a.Credentials != b.Credentials || len(a.Listeners) != len(b.Listeners) {
		return false
	}
	for i := range a.Listeners {
		if a.Listeners[i] != b.Listeners[i] {
			return false
		}
	}
	return true
}

func sameListenerIntent(applied listenerConfig, node *domain.Node) bool {
	return node != nil && applied == node.ConfigIntent()
}

func objectStatuses(all []nodeprotocol.ObjectStatus, stream string) map[string]nodeprotocol.ObjectStatus {
	out := make(map[string]nodeprotocol.ObjectStatus)
	for _, status := range all {
		if status.Stream == stream {
			out[status.Key] = status
		}
	}
	return out
}

func clientApplyState(state nodeprotocol.ObjectState) domain.ClientApplyState {
	switch state {
	case nodeprotocol.ObjectApplied:
		return domain.ClientApplyApplied
	case nodeprotocol.ObjectRejected:
		return domain.ClientApplyRejected
	case nodeprotocol.ObjectBlocked:
		return domain.ClientApplyBlocked
	default:
		return domain.ClientApplyPending
	}
}

func cloneReport(in nodeprotocol.NodeReport) nodeprotocol.NodeReport {
	out := in
	out.Have = make(map[string]nodeprotocol.StreamState, len(in.Have))
	for key, value := range in.Have {
		out.Have[key] = value
	}
	out.Objects = append([]nodeprotocol.ObjectStatus(nil), in.Objects...)
	out.ListenerCounters = append([]nodeprotocol.ListenerCounters(nil), in.ListenerCounters...)
	out.Clients = append([]nodeprotocol.ClientCounters(nil), in.Clients...)
	for i := range out.Clients {
		out.Clients[i].LiveIPs = append([]string(nil), in.Clients[i].LiveIPs...)
	}
	out.Subjects = append([]nodeprotocol.SubjectObservation(nil), in.Subjects...)
	out.Issues = append([]nodeprotocol.Issue(nil), in.Issues...)
	out.TaskResults = append([]nodeprotocol.TaskResult(nil), in.TaskResults...)
	for i := range out.TaskResults {
		out.TaskResults[i].Result = append([]byte(nil), in.TaskResults[i].Result...)
	}
	out.Capabilities = append([]string(nil), in.Capabilities...)
	// The policy status is a control input consumed before the response is
	// built; nothing later reads it, so it is not kept.
	out.PolicyStatus = nil
	// Host is cleared EXPLICITLY as a second line of defence. The caller already
	// removes it before this is reached, but a clone that silently carried 128 KiB
	// per agent into the latest-full cache is the kind of regression that only
	// shows up as memory, months later.
	out.Host = nil
	return out
}

// cloneObservationReport keeps only state consumed after the request ends.
// Issues and task results are one-shot durable events, while capabilities gate
// only the current response. Retaining any of them in the latest-full cache
// would pin up to several MiB per offline agent for no later computation.
func cloneObservationReport(in nodeprotocol.NodeReport) nodeprotocol.NodeReport {
	out := cloneReport(in)
	out.Issues = nil
	out.TaskResults = nil
	out.Capabilities = nil
	out.Host = nil
	return out
}

func (s *Service) offerTasks(ctx context.Context, agentID string, capabilities []string, maxTaskJSONBytes int, now time.Time) ([]nodeprotocol.Task, bool, error) {
	support := taskOfferSupport(capabilities)
	// Even an old/capability-less agent or a full response budget must close
	// expired dispatch. Expiry does not depend on permission to offer work.
	stored, err := s.tasks.Offer(ctx, agentID, support, nodeprotocol.MaxTasksPerResponse, maxTaskJSONBytes, now)
	if err != nil {
		return nil, false, fmt.Errorf("nodesync: offer agent tasks: %w", err)
	}
	tasks := make([]nodeprotocol.Task, len(stored))
	hadFirstOffer := false
	for i := range stored {
		if stored[i] == nil {
			return nil, false, fmt.Errorf("%w: task repository returned an empty identity", domain.ErrConflict)
		}
		deadline := int64(0)
		if stored[i].Lifecycle != nil {
			if !support.SupportsExpiry || stored[i].Lifecycle.Validate() != nil ||
				stored[i].Lifecycle.NotAfterMS <= now.UnixMilli() {
				return nil, false, fmt.Errorf("%w: lifecycle-aware task lacks valid current start authorization", domain.ErrConflict)
			}
			deadline = stored[i].Lifecycle.NotAfterMS
		}
		if !slices.Contains(support.EligibleKinds, stored[i].Kind) {
			return nil, false, fmt.Errorf("%w: task kind is not supported by this report", domain.ErrConflict)
		}
		if stored[i].OfferCount == 1 {
			hadFirstOffer = true
		}
		tasks[i] = nodeprotocol.Task{
			ID: stored[i].TaskID, Kind: stored[i].Kind,
			Args: append([]byte(nil), stored[i].Args...), InputSHA256: stored[i].InputSHA256,
			NotAfterMS: deadline,
		}
	}
	if err := nodeprotocol.ValidateTasks(tasks); err != nil {
		return nil, false, fmt.Errorf("nodesync: stored agent tasks are invalid: %w", err)
	}
	return tasks, hadFirstOffer, nil
}

func availableTaskJSONBytes(response nodeprotocol.SyncResponse, responseLimit int64) (int, error) {
	response.Tasks = nil
	encoded, err := json.Marshal(response)
	if err != nil {
		return 0, fmt.Errorf("nodesync: encode response without tasks: %w", err)
	}
	if int64(len(encoded)) > responseLimit {
		return 0, fmt.Errorf("nodesync: response without tasks exceeds %d bytes", responseLimit)
	}
	// Keep task offers within the plain budget. Large compressed control
	// responses take priority and tasks can be offered on the next poll.
	remaining := int(nodeprotocol.MaxSyncWireBytes) - len(encoded) - syncTasksFieldJSONBytes
	if remaining < len("[]") {
		return 0, nil
	}
	return remaining, nil
}

func eligibleTaskKinds(capabilities []string) []string {
	capabilitySet := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		capabilitySet[capability] = struct{}{}
	}
	if !hasTaskExecutionCapability(capabilities) {
		return nil
	}
	prefix := nodeprotocol.TaskCapability("")
	kinds := make([]string, 0, len(capabilities))
	for capability := range capabilitySet {
		if capability == nodeprotocol.CapabilityTaskExecutionV1 || capability == nodeprotocol.CapabilityTaskExpiryV1 || !strings.HasPrefix(capability, prefix) {
			continue
		}
		kind := strings.TrimPrefix(capability, prefix)
		if kind != "" && nodeprotocol.TaskCapability(kind) == capability {
			kinds = append(kinds, kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}

func taskOfferSupport(capabilities []string) ports.NodeAgentTaskOfferSupport {
	return ports.NodeAgentTaskOfferSupport{
		EligibleKinds:  eligibleTaskKinds(capabilities),
		SupportsExpiry: hasTaskExecutionCapability(capabilities) && slices.Contains(capabilities, nodeprotocol.CapabilityTaskExpiryV1),
	}
}

func hasTaskExecutionCapability(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == nodeprotocol.CapabilityTaskExecutionV1 {
			return true
		}
	}
	return false
}

func (s *Service) buildDirectives(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, current nodeprotocol.NodeReport, rosterVersion nodeprotocol.Version, now time.Time) (nodeprotocol.DirectivesBody, nodeprotocol.Envelope, error) {
	settings, err := s.settings.Load(ctx, ports.UISettings{
		NodePollSeconds: nodeprotocol.DefaultNextPollSeconds, FullReportSeconds: nodeprotocol.DefaultFullReportSeconds,
	})
	if err != nil {
		return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: load settings: %w", err)
	}
	periodNow := now
	if settings.Timezone != "" {
		loc, loadErr := time.LoadLocation(settings.Timezone)
		if loadErr != nil {
			return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: load timezone %q: %w", settings.Timezone, loadErr)
		}
		periodNow = now.In(loc)
	}
	nextPollSeconds := settings.NodePollSeconds
	if nextPollSeconds <= 0 {
		nextPollSeconds = nodeprotocol.DefaultNextPollSeconds
	}
	fullFreshnessSeconds := nodeprotocol.EffectiveFullReportPeriod(settings.FullReportSeconds, nextPollSeconds)
	report, receivedAtMS, hasFull := s.fullReport(agent.AgentID)
	if !current.Partial {
		report, hasFull = current, true
	} else if hasFull && fullReportStale(receivedAtMS, now, fullFreshnessSeconds) {
		hasFull = false
	}
	allClients, err := s.clients.ListAll(ctx)
	if err != nil {
		return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: list quota clients: %w", err)
	}
	agents, err := s.agents.List(ctx)
	if err != nil {
		return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: list native agents: %w", err)
	}
	clientByID := make(map[int64]*domain.PSPClient, len(allClients))
	for _, client := range allClients {
		if client != nil {
			clientByID[client.ID] = client
		}
	}
	// Line factors of this server's nodes. The user ledger and pending usage
	// are in CHARGED bytes; a node gates each client on its own RAW counter, so
	// every grant below is converted through that client's line factor.
	factors := domain.LineFactors(snapshot.Nodes)
	pendingByUser := s.pendingUsage(clientByID, agents, factors)
	counters := make(map[int64]nodeprotocol.ClientCounters)
	if hasFull {
		for _, counter := range report.Clients {
			if id, parseErr := counter.Key.RowID(); parseErr == nil {
				counters[id] = counter
			}
		}
	}
	body := nodeprotocol.DirectivesBody{
		ForRosterVersion: rosterVersion,
		Quota:            make([]nodeprotocol.QuotaEntry, 0, len(snapshot.Clients)),
	}
	users := make(map[int64]*domain.User)
	emergencyQuotaGBByUser := make(map[int64]float64)
	for _, desired := range snapshot.Clients {
		client := desired.Client
		if client == nil {
			continue
		}
		user := users[client.UserID]
		if user == nil {
			user, err = s.users.GetByID(ctx, client.UserID)
			if err != nil {
				return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: load user %d: %w", client.UserID, err)
			}
			users[client.UserID] = user
		}
		counter, counterKnown := counters[client.ID]
		baseline := int64(0)
		if counterKnown {
			baseline = nonNegativeSum(counter.UpBytes, counter.DownBytes)
		}
		entry := nodeprotocol.QuotaEntry{Client: nodeprotocol.NewClientKey(client.ID), BaselineBytes: baseline}
		factor := domain.ClientTrafficFactor(factors, client)
		if user.EmergencyActive(now) {
			// Emergency access deliberately overrides an already-exhausted normal
			// period. Re-measure from the lifetime snapshot taken when the window
			// opened, and include fleet usage observed by agents but not yet folded
			// into the user row. This is the native equivalent of user.emergencyFloor.
			//
			// A zero emergency quota is an intentional unlimited window, represented
			// by nil headroom. A finite quota still fails closed until this agent has
			// supplied its current cumulative counter, exactly like the normal limit.
			quotaGB, loaded := emergencyQuotaGBByUser[user.ID]
			if !loaded {
				effective, loadErr := s.settings.LoadForUser(ctx, user, ports.UISettings{
					EmergencyAccessQuotaGB: settings.EmergencyAccessQuotaGB,
				})
				if loadErr != nil {
					return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: load emergency settings for user %d: %w", user.ID, loadErr)
				}
				quotaGB = effective.EmergencyAccessQuotaGB
				emergencyQuotaGBByUser[user.ID] = quotaGB
			}
			if quotaGB > 0 {
				quotaBytes := int64(quotaGB * float64(int64(1)<<30))
				used := user.LifetimeTotalBytes - user.EmergencyBaselineBytes
				if used < 0 {
					used = 0
				}
				remaining := saturatingSub(quotaBytes, saturatingAdd(used, pendingByUser[user.ID]))
				entry.HeadroomBytes = nativeHeadroom(remaining, factor, hasFull && counterKnown)
			}
		} else if user.TrafficLimitBytes > 0 {
			// A limited client whose current cumulative counter is absent from a
			// full report is closed. An explicitly reported counter remains known
			// when Present=false: that flag means the local quota gate omitted the
			// client from the running core, not that its durable counter vanished.
			// Requiring Present here deadlocks first installation: PSP sends zero
			// headroom while the counter is initially unknown, the node closes the
			// client, then every subsequent report says Present=false and PSP never
			// sends the non-zero grant that could open it.
			remaining := saturatingSub(user.TrafficLimitBytes, saturatingAdd(user.PeriodUsed(), pendingByUser[user.ID]))
			entry.HeadroomBytes = nativeHeadroom(remaining, factor, hasFull && counterKnown)
		}
		// The emergency window changes the current grant, not the next regular
		// period. Keep the advance rollover authorization so an offline panel does
		// not leave the user closed when a new paid period begins.
		//
		// A free server (factor 0) gets no advance grant: its gate is open
		// while the user has quota left, and an exhausted user stays closed
		// until PSP is back, which is the side a missed refresh is meant to
		// fail on. The protocol has no way to schedule "open" anyway.
		if next, ok := domain.RawBytesFor(user.TrafficLimitBytes, factor); user.TrafficLimitBytes > 0 && ok {
			if periodEnd := nextPeriodEnd(periodNow, user.TrafficResetPeriod); !periodEnd.IsZero() {
				entry.PeriodEndsAtMS = periodEnd.UnixMilli()
				entry.NextPeriodHeadroomBytes = int64Pointer(next)
			}
		}
		body.Quota = append(body.Quota, entry)
	}
	userIDs := make([]int64, 0, len(users))
	for id := range users {
		userIDs = append(userIDs, id)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	for _, id := range userIDs {
		body.IPShadow = append(body.IPShadow, nodeprotocol.IPShadowEntry{
			Subject: nodeprotocol.NewSubjectKey(id), IPLimit: users[id].IPLimit,
		})
	}
	coverage, oldestReportMS := s.aggregateCoverage(allClients, agents, fullFreshnessSeconds, now)
	body.Coverage = coverage
	envelope := nodeprotocol.Envelope{
		ComputedAtMS: now.UnixMilli(), NextPollSeconds: nextPollSeconds,
		FullReportSeconds: settings.FullReportSeconds, WantFullReport: !hasFull,
	}
	// Zero would mean "carry a sample on every report", so it is only left unset
	// when this build has no ingest at all — in which case the panel has nothing
	// to ask for and must not tell the node to report anything.
	if s.host != nil {
		envelope.HostReportSeconds = int(nodemetrics.DefaultNodeHostReportInterval / time.Second)
	}
	envelope.NumeratorAsOfMS, envelope.NumeratorOldestReportAgeMS,
		envelope.OverburnHeadroomBytes = s.recordGrantsAndFleetEnvelope(agent.AgentID, body.Quota, now, oldestReportMS)
	return body, envelope, nil
}

// aggregateCoverage derives the denominator from PSP's own native-panel client
// rows, never from however many counters happened to arrive. EntriesStale is a
// tag only: it must not reduce Entries or any quota/error bound.
func (s *Service) aggregateCoverage(clients []*domain.PSPClient, agents []*domain.NodeAgent, fullFreshnessSeconds int, now time.Time) (nodeprotocol.SegmentCounts, int64) {
	agentByPanel := make(map[int64]string, len(agents))
	for _, agent := range agents {
		if agent != nil {
			agentByPanel[agent.PanelID] = agent.AgentID
		}
	}
	s.mu.RLock()
	reports := make(map[string]receivedFullReport, len(s.reports))
	for agentID, received := range s.reports {
		received.report = cloneReport(received.report)
		reports[agentID] = received
	}
	s.mu.RUnlock()

	type reportIndex struct {
		stale    bool
		counters map[nodeprotocol.ClientKey]bool
		atMS     int64
	}
	indexed := make(map[string]reportIndex, len(agentByPanel))
	for _, agentID := range agentByPanel {
		received, ok := reports[agentID]
		idx := reportIndex{stale: !ok || received.receivedAtMS <= 0, atMS: received.receivedAtMS}
		idx.stale = idx.stale || fullReportStale(received.receivedAtMS, now, fullFreshnessSeconds)
		if received.receivedAtMS > now.UnixMilli() {
			// A PSP wall-clock rollback makes the age unknowable. Keep the rows
			// stale and publish an unknown aggregate timestamp instead of a
			// future numerator time with a deceptively zero age.
			idx.atMS = 0
		}
		idx.counters = make(map[nodeprotocol.ClientKey]bool, len(received.report.Clients))
		for _, counter := range received.report.Clients {
			idx.counters[counter.Key] = counter.Present
		}
		indexed[agentID] = idx
	}

	coverage := nodeprotocol.SegmentCounts{}
	subjects := make(map[int64]struct{})
	usedAgents := make(map[string]struct{})
	for _, client := range clients {
		if client == nil {
			continue
		}
		agentID, native := agentByPanel[client.PanelID]
		if !native {
			continue
		}
		coverage.Entries++
		subjects[client.UserID] = struct{}{}
		usedAgents[agentID] = struct{}{}
		idx := indexed[agentID]
		if idx.stale || !idx.counters[nodeprotocol.NewClientKey(client.ID)] {
			coverage.EntriesStale++
		}
	}
	coverage.Subjects = len(subjects)

	var oldest int64
	for agentID := range usedAgents {
		idx := indexed[agentID]
		if idx.atMS <= 0 {
			return coverage, 0
		}
		if oldest == 0 || idx.atMS < oldest {
			oldest = idx.atMS
		}
	}
	return coverage, oldest
}

// recordGrantsAndFleetEnvelope records the actual residual grants PSP most
// recently sent each agent, then reports fleet-wide exposure and the oldest
// full-report timestamp in the same critical section. A per-agent sum would
// look precise while hiding the exact P× overburn risk this metric exists to
// expose.
func (s *Service) recordGrantsAndFleetEnvelope(agentID string, quotas []nodeprotocol.QuotaEntry, now time.Time, oldest int64) (int64, int64, int64) {
	current := make(map[nodeprotocol.ClientKey]int64)
	for _, quota := range quotas {
		if quota.HeadroomBytes != nil {
			current[quota.Client] = *quota.HeadroomBytes
		}
	}
	nowMS := now.UnixMilli()
	s.mu.Lock()
	s.grants[agentID] = current
	var overburn int64
	for _, byClient := range s.grants {
		for _, headroom := range byClient {
			overburn = saturatingAdd(overburn, headroom)
		}
	}
	s.mu.Unlock()
	var age int64
	if oldest > 0 && nowMS > oldest {
		age = nowMS - oldest
	}
	return oldest, age, overburn
}

func (s *Service) fullReport(agentID string) (nodeprotocol.NodeReport, int64, bool) {
	s.mu.RLock()
	received, ok := s.reports[agentID]
	s.mu.RUnlock()
	return cloneReport(received.report), received.receivedAtMS, ok
}

func fullReportStale(receivedAtMS int64, now time.Time, freshnessSeconds int) bool {
	if receivedAtMS <= 0 || freshnessSeconds <= 0 {
		return true
	}
	nowMS := now.UnixMilli()
	return nowMS < receivedAtMS || nowMS-receivedAtMS > int64(freshnessSeconds)*1000
}

// nativeHeadroom converts a user's remaining quota, in CHARGED bytes, into
// the headroom a node with this server factor gates its RAW counter on.
//
//   - factor > 0: floor(remaining × 1000 / factor). Rounding down closes the
//     gate no later than the charged ledger would (fail-closed). A counter the
//     node has not reported yet is closed (0), exactly as before.
//   - factor 0: the server is free. No gate (nil) while the user has quota
//     left, whether or not the counter is known — no amount of traffic here
//     can use it up — and closed (0) once it is exhausted elsewhere.
func nativeHeadroom(remainingCharged int64, factor int, counterKnown bool) *int64 {
	raw, ok := domain.RawBytesFor(remainingCharged, factor)
	if !ok {
		if remainingCharged > 0 {
			return nil
		}
		return int64Pointer(0)
	}
	if !counterKnown {
		return int64Pointer(0)
	}
	return int64Pointer(raw)
}

// pendingUsage is each user's usage observed by agents but not yet folded into
// the user row, in CHARGED bytes: each client's raw pending delta weighted by
// its line's factor, like the poll will book it. A client on another server,
// whose lines are not in factors, is weighted at the factor it was last booked
// at.
func (s *Service) pendingUsage(clients map[int64]*domain.PSPClient, agents []*domain.NodeAgent, factors map[domain.LineKey]int) map[int64]int64 {
	panelByAgent := make(map[string]int64, len(agents))
	for _, agent := range agents {
		if agent != nil {
			panelByAgent[agent.AgentID] = agent.PanelID
		}
	}
	s.mu.RLock()
	reports := make(map[string]nodeprotocol.NodeReport, len(s.reports))
	for agentID, received := range s.reports {
		reports[agentID] = cloneReport(received.report)
	}
	s.mu.RUnlock()
	result := make(map[int64]int64)
	for agentID, report := range reports {
		panelID, active := panelByAgent[agentID]
		if !active {
			continue
		}
		for _, counter := range report.Clients {
			id, err := counter.Key.RowID()
			if err != nil {
				continue
			}
			client := clients[id]
			// Authentication scopes the request to one agent, but client keys are
			// still untrusted wire data. Bind every pending delta back to PSP's
			// durable agent→panel and client→panel ownership before it can affect
			// another user's fleet-wide quota.
			if client == nil || client.PanelID != panelID {
				continue
			}
			pending := domain.ChargeBytes(s.pendingCounterDelta(client, counter), domain.ClientTrafficFactor(factors, client))
			result[client.UserID] = saturatingAdd(result[client.UserID], pending)
		}
	}
	return result
}

func (s *Service) pendingCounterDelta(client *domain.PSPClient, counter nodeprotocol.ClientCounters) int64 {
	if counter.CounterEpoch == 0 {
		return 0
	}
	baseUp, baseDown := client.LastRawUpBytes, client.LastRawDownBytes
	s.mu.Lock()
	if client.LastCounterEpoch == counter.CounterEpoch {
		delete(s.anchors, client.ID)
	} else {
		anchor, exists := s.anchors[client.ID]
		if !exists || anchor.CounterEpoch != counter.CounterEpoch {
			s.anchors[client.ID] = counter
			s.mu.Unlock()
			return 0
		}
		baseUp, baseDown = anchor.UpBytes, anchor.DownBytes
	}
	s.mu.Unlock()
	up := counter.UpBytes - baseUp
	down := counter.DownBytes - baseDown
	if up < 0 {
		up = 0
	}
	if down < 0 {
		down = 0
	}
	return saturatingAdd(up, down)
}

func nextPeriodEnd(now time.Time, period domain.ResetPeriod) time.Time {
	switch period {
	case domain.ResetMonthly:
		return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	case domain.ResetQuarterly:
		month := time.Month(((int(now.Month())-1)/3)*3 + 1)
		return time.Date(now.Year(), month+3, 1, 0, 0, 0, 0, now.Location())
	case domain.ResetYearly:
		return time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, now.Location())
	default:
		return time.Time{}
	}
}

func int64Pointer(value int64) *int64 { return &value }

func nonNegativeSum(a, b int64) int64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	return saturatingAdd(a, b)
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func saturatingSub(limit, used int64) int64 {
	if used >= limit {
		return 0
	}
	return limit - used
}

// NativePanelSnapshot projects only a recent cached full observation from a
// live agent. A partial report never turns absent enumerations into deletion
// or zero traffic, and stale bytes never remain readable indefinitely.
func (s *Service) NativePanelSnapshot(ctx context.Context, panelID int64) (*ports.NativePanelSnapshot, error) {
	agent, err := s.agents.GetByPanelID(ctx, panelID)
	if err != nil {
		return nil, err
	}
	report, receivedAtMS, ok := s.fullReport(agent.AgentID)
	if !ok {
		return nil, ports.ErrNativePanelSnapshotMissing
	}
	settings, err := s.settings.Load(ctx, ports.UISettings{
		NodePollSeconds: nodeprotocol.DefaultNextPollSeconds, FullReportSeconds: nodeprotocol.DefaultFullReportSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("native panel snapshot: load cadence: %w", err)
	}
	pollSeconds := settings.NodePollSeconds
	if pollSeconds <= 0 {
		pollSeconds = nodeprotocol.DefaultNextPollSeconds
	}
	now := s.now().UTC()
	// Liveness is based only on the durable receipt time of any heartbeat.
	// Three missed polls absorb ordinary jitter without letting a dead agent's
	// last snapshot look live forever.
	if agent.OfflineAt(now, 3*time.Duration(pollSeconds)*time.Second) {
		return nil, ports.ErrNativePanelAgentOffline
	}
	// Full enumerations are independently paced. Allow one additional poll of
	// scheduling/network jitter beyond the effective discrete full cadence;
	// after that, absent objects and counters are unknowable, not healthy zeroes.
	fullFreshnessSeconds := nodeprotocol.EffectiveFullReportPeriod(settings.FullReportSeconds, pollSeconds) + pollSeconds
	if fullReportStale(receivedAtMS, now, fullFreshnessSeconds) {
		return nil, ports.ErrNativePanelSnapshotStale
	}
	desired, err := s.desired.Load(ctx, panelID)
	if err != nil {
		return nil, err
	}
	configStream, err := s.agents.GetStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig)
	if err != nil {
		return nil, err
	}
	rosterStream, err := s.agents.GetStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster)
	if err != nil {
		return nil, err
	}
	configHave := report.Have[nodeprotocol.StreamConfig]
	rosterHave := report.Have[nodeprotocol.StreamRoster]
	configCurrent := configHave.Applied.Epoch == agent.Epoch && string(configHave.ETag) == configStream.DesiredETag
	rosterCurrent := rosterHave.Applied.Epoch == agent.Epoch && string(rosterHave.ETag) == rosterStream.DesiredETag
	statusByStream := make(map[string]map[string]nodeprotocol.ObjectStatus)
	statusByStream[nodeprotocol.StreamConfig] = objectStatuses(report.Objects, nodeprotocol.StreamConfig)
	statusByStream[nodeprotocol.StreamRoster] = objectStatuses(report.Objects, nodeprotocol.StreamRoster)
	listenerCounters := make(map[int64]nodeprotocol.ListenerCounters)
	for _, counter := range report.ListenerCounters {
		if id, parseErr := counter.Key.RowID(); parseErr == nil {
			listenerCounters[id] = counter
		}
	}
	clientCounters := make(map[int64]nodeprotocol.ClientCounters)
	for _, counter := range report.Clients {
		if id, parseErr := counter.Key.RowID(); parseErr == nil {
			clientCounters[id] = counter
		}
	}
	nodesByID := make(map[int64]*domain.Node, len(desired.Nodes))
	result := &ports.NativePanelSnapshot{
		Clients: make(map[string]ports.ClientDetail), LiveClientIPs: make(map[string][]string),
		Status: ports.ServerStatus{
			PanelVersion: report.AgentVersion, CoreEngine: report.CoreEngine,
			XrayVersion: report.CoreVersion, XrayState: report.CoreState,
		},
	}
	for _, node := range desired.Nodes {
		if node == nil {
			continue
		}
		nodesByID[node.ID] = node
		counter, present := listenerCounters[node.ID]
		object := statusByStream[nodeprotocol.StreamConfig][string(nodeprotocol.NewListenerKey(node.ID))]
		if !configCurrent || !present || !counter.Present || object.State != nodeprotocol.ObjectApplied {
			continue
		}
		result.Inbounds = append(result.Inbounds, ports.Inbound{
			ID: node.InboundID, Up: counter.UpBytes, Down: counter.DownBytes,
			Total: nonNegativeSum(counter.UpBytes, counter.DownBytes), CounterEpoch: counter.CounterEpoch,
			Remark: node.InboundRemark,
			Enable: node.Enabled, ExpiryTime: node.InboundExpiryTime,
			Listen: node.InboundListen, Port: node.DesiredPort, Protocol: node.DesiredProtocol,
			Settings: node.InboundSettings, StreamSettings: node.StreamSettings,
			Sniffing: node.Sniffing, Allocate: node.Allocate,
		})
	}
	for _, desiredClient := range desired.Clients {
		client := desiredClient.Client
		if client == nil {
			continue
		}
		counter, reported := clientCounters[client.ID]
		object := statusByStream[nodeprotocol.StreamRoster][string(nodeprotocol.NewClientKey(client.ID))]
		if !rosterCurrent || !reported || object.State != nodeprotocol.ObjectApplied {
			continue
		}
		// Present describes membership in the running core, not whether the
		// applied roster identity exists. A locally closed quota gate removes a
		// client from Xray while retaining its durable identity and counters. The
		// PanelClient facade must still expose that identity so lifecycle writes,
		// subscription credential pinning and final traffic accounting converge.
		detail := ports.ClientDetail{
			ID: client.UUID, Email: client.Email, Enable: client.DesiredEnable,
			Password: client.Password, Auth: client.UUID, ExpiryTime: client.DesiredExpiryTime,
			LimitIP: client.PanelIPLimit, LimitHwid: client.PanelDeviceLimit,
		}
		for _, attachment := range desiredClient.Inbounds {
			if node := nodesByID[attachment.NodeID]; node != nil {
				detail.InboundIDs = append(detail.InboundIDs, node.InboundID)
				if attachment.FlowOverride != "" {
					detail.Flow = attachment.FlowOverride
				}
			}
		}
		sort.Ints(detail.InboundIDs)
		result.Clients[client.Email] = detail
		if len(counter.LiveIPs) > 0 {
			ips := append([]string(nil), counter.LiveIPs...)
			sort.Strings(ips)
			result.LiveClientIPs[client.Email] = compactStrings(ips)
		}
		for i := range result.Inbounds {
			if containsInt(detail.InboundIDs, result.Inbounds[i].ID) {
				result.Inbounds[i].ClientStats = append(result.Inbounds[i].ClientStats, ports.ClientTraffic{
					InboundID: result.Inbounds[i].ID, Email: client.Email,
					Up: counter.UpBytes, Down: counter.DownBytes,
					Total:  nonNegativeSum(counter.UpBytes, counter.DownBytes),
					Enable: client.DesiredEnable, ExpiryTime: client.DesiredExpiryTime,
					CounterEpoch: counter.CounterEpoch,
				})
			}
		}
	}
	return result, nil
}

func compactStrings(items []string) []string {
	out := items[:0]
	for _, item := range items {
		if item == "" || (len(out) > 0 && out[len(out)-1] == item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func containsInt(items []int, want int) bool {
	i := sort.SearchInts(items, want)
	return i < len(items) && items[i] == want
}

var _ ports.NativePanelSnapshotReader = (*Service)(nil)

// recordSourceGuardHits keeps the sources a landing refused, from a full report
// of an agent that declared the capability. A hit names a set; it is kept only
// when the set is one of this server's own landings, so an agent cannot put a
// warning on a line of another server.
func (s *Service) recordSourceGuardHits(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, report nodeprotocol.NodeReport, now time.Time) error {
	if s.hits == nil || !slices.Contains(report.Capabilities, nodeprotocol.CapabilitySourceGuard) {
		return nil
	}
	own := make(map[int64]bool, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node != nil {
			own[node.ID] = true
		}
	}
	hits := make([]domain.SourceGuardHit, 0, len(report.SourceGuardHits))
	var issues []domain.NodeAgentIssue
	for _, hit := range report.SourceGuardHits {
		listener, err := hit.Listener.RowID()
		if err != nil || !own[sourceguard.NodeIDOf(string(hit.Set))] || !own[listener] {
			continue
		}
		hits = append(hits, domain.SourceGuardHit{
			PanelID: agent.PanelID, Set: string(hit.Set), ListenerNodeID: listener, IP: hit.SourceIP,
			Count: hit.Count, LastSeen: time.UnixMilli(hit.LastSeenMS).UTC(),
		})
		// The issue names the line and the address and nothing that changes with
		// the count, so a source that keeps being refused is one issue whose last
		// seen time moves, not a new one at every report.
		for _, node := range snapshot.Nodes {
			if node == nil || node.ID != sourceguard.NodeIDOf(string(hit.Set)) {
				continue
			}
			for _, line := range node.Relays {
				if sourceguard.Key(node.ID, line.ID) == hit.Set {
					issues = append(issues, domain.NodeAgentIssue{
						AgentID: agent.AgentID, Code: IssueSourceGuardRefused, Key: string(hit.Set),
						Detail: fmt.Sprintf("line %q of %s refused connections from %s, which is not one of its source addresses", line.Label(), node.DisplayName, hit.SourceIP),
					})
				}
			}
		}
	}
	if len(issues) > 0 {
		if err := s.issues.RecordBatch(ctx, agent.AgentID, issues, now); err != nil {
			return fmt.Errorf("nodesync: record source guard issues: %w", err)
		}
	}
	if err := s.hits.RecordHits(ctx, agent.PanelID, hits, now.Add(-sourceGuardHitRetention), sourceGuardHitLimit); err != nil {
		return fmt.Errorf("nodesync: record source guard hits: %w", err)
	}
	return nil
}
