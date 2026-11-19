// Package node implements panel-side Node CRUD plus the two flows that
// reach into 3X-UI:
//
//   - Import existing inbound: pure metadata insert, zero 3X-UI writes.
//   - Create new inbound: AddInbound → record metadata.
//
// After either creation flow, syncExistingUsersToNode walks every panel group whose
// tag_filter would include the new node and pushes a client per group
// member through SyncSvc — so admins don't have to manually "resync" each
// user after every node addition.
//
// Deletion goes through sync.Service so the write guards (inbound must end
// up empty before being deleted) apply uniformly.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/xraycompat"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/xrayspec"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/inboundcfg"
)

// InboundCleaner is the narrow subset of sync.Service used by node deletion.
// Defined here so the node package never imports sync.
type InboundCleaner interface {
	DelAllOwnedForInbound(ctx context.Context, panelID int64, inboundID int) error
	UnclaimAllForInbound(ctx context.Context, panelID int64, inboundID int) error
	EnsureInboundDeletable(ctx context.Context, panelID int64, inboundID int) error
	DeleteInbound(ctx context.Context, panelID int64, inboundID int) error
}

// MemberResyncer re-provisions a user's shared clients — immediately, falling back
// to the sync-task queue on failure (so a flaky 3X-UI call isn't dropped, it's
// retried). Implemented by user.Service.ResyncMembershipOrEnqueue; late-bound so the
// node package never imports user. nil before wiring / in tests.
type MemberResyncer interface {
	ResyncMembershipOrEnqueue(ctx context.Context, userID int64, summary string) error
	// BulkProvisionNodeMembers front-loads many members' shared-client creates onto
	// this node's inbound in ONE bulkCreate + bulkAttach (one Xray restart) before
	// the per-member resyncs run. Best-effort warm-up; a failure is logged and the
	// per-member ResyncMembershipOrEnqueue pass still provisions authoritatively.
	BulkProvisionNodeMembers(ctx context.Context, n *domain.Node, userIDs []int64) error
}

type Service struct {
	nodes ports.NodeRepo
	// fronts holds the SNI fronts a save is checked against (SetFronts).
	fronts   ports.TransitFrontRepo
	frontSvc *front.Service
	// afterDelete removes the tunnels only a deleted node's routes used.
	afterDelete func(ctx context.Context, node *domain.Node) error
	// routeLock is the routing service's lock of a server: a save of routes holds
	setupRouter SetupRouter
	// it, and so does a delete of a node, so that the cleanup of the tunnels only
	// the node's routes used cannot miss an exit a concurrent save just added.
	routeLock  func(panelID int64) func()
	separators ports.SeparatorRepo
	pool       ports.XUIPool
	cleaner    InboundCleaner
	tasks      ports.SyncTaskRepo
	groups     ports.GroupRepo
	users      ports.UserRepo
	resyncer   MemberResyncer
	// eligibility decides whether a group's members get a client on a node
	// (group.Service.Eligible). Late-bound; nil is MatchOnly.
	eligibility GroupEligibility
	// allowlistResync is told which panel's inbounds changed in a way that can
	// change what the panel can enforce for a group allowlist
	// (SetAllowlistResyncer). Nil is silent.
	allowlistResync func(ctx context.Context, panelID int64)
	// bg routes handler-spawned background work (member provisioning after a node
	// recreate; sync-existing-users after a node add) through the app's tracked
	// dispatcher so App.Shutdown drains it under a cancellable context. Late-bound
	// via SetBackgroundRunner (mirrors user.Service); nil before wiring / in tests,
	// where runBackground falls back to an untracked safego.Go.
	bg func(name string, fn func(ctx context.Context))
	// invalidateSubscriptions is late-bound by app wiring because the node
	// package must not import the group or render services. It fires after any
	// successful write that changes what a subscription renders.
	invalidateSubscriptions func()
	// repoWrapped guards SetSubscriptionInvalidator against stacking a second
	// decorator on a re-wire.
	repoWrapped bool
	// tunnelLinks makes the tunnel a new tunnel line leaves through (tunnel.go).
	tunnelLinks TunnelLinker
	// planned are the ports picked for listeners about to be made, by line.
	plannedMu sync.Mutex
	planned   map[string]int
	// orderMu serialises subscription reorders so the version check and the
	// write of one cannot interleave with another's.
	orderMu sync.Mutex
	// canGuard says whether a landing on a server can check where connections come
	// from (SetSourceGuardCheck).
	canGuard func(ctx context.Context, panelID int64) bool
	// guardInputs are the facts the source guard rules are judged by
	// (SetSourceGuardInputs).
	guardInputs func(ctx context.Context, nodes []*domain.Node) (domain.SourceInputs, error)
}

// GroupEligibility is the one test of "does this group include this node"
// (group.Service.Eligible): the tag filter and, for a group with a destination
// allowlist, whether the node can enforce it.
type GroupEligibility interface {
	Eligible(ctx context.Context, n *domain.Node, g *domain.Group) (bool, error)
}

// SetGroupEligibility late-binds the eligibility test the add paths use.
func (s *Service) SetGroupEligibility(e GroupEligibility) { s.eligibility = e }

// GroupEligibilityWired reports whether an eligibility has been bound, for the
// application's wiring test: unbound, the add paths decide by tags alone.
func (s *Service) GroupEligibilityWired() bool { return s.eligibility != nil }

// SetAllowlistResyncer late-binds what runs when a panel's inbound sniffing or
// enabled state changes: group.Service.InboundsChanged, which decides whether
// the panel is one that enforces allowlists and resyncs the members if so.
func (s *Service) SetAllowlistResyncer(resync func(ctx context.Context, panelID int64)) {
	s.allowlistResync = resync
}

func (s *Service) inboundsChanged(ctx context.Context, panelID int64) {
	if s.allowlistResync != nil {
		s.allowlistResync(ctx, panelID)
	}
}

// groupIncludes asks the wired eligibility, or only the tag filter before one
// is wired. An error means the answer is unknown: the add paths skip the group
// this round, which is the safe direction (nothing is added; the next round or
// the heal sweep adds what is still missing), and log the group id alone.
func (s *Service) groupIncludes(ctx context.Context, n *domain.Node, g *domain.Group) bool {
	var e GroupEligibility = group.MatchOnly{}
	if s.eligibility != nil {
		e = s.eligibility
	}
	ok, err := e.Eligible(ctx, n, g)
	if err != nil {
		log.Warn("group eligibility unreadable; skipping the group this round", "group_id", g.ID)
		return false
	}
	return ok
}

// SetMemberResyncer late-binds the shared-client member resyncer (user.Service).
func (s *Service) SetMemberResyncer(r MemberResyncer) { s.resyncer = r }

// SetBackgroundRunner late-binds the app's tracked async dispatcher so node
// background work is drained by App.Shutdown instead of leaking on an untracked
// goroutine (mirrors user.Service.SetBackgroundRunner).
func (s *Service) SetBackgroundRunner(run func(name string, fn func(ctx context.Context))) {
	s.bg = run
}

// SetSubscriptionInvalidator wires cache invalidation for subscription-visible
// node and separator mutations without coupling this package to render.
//
// Wiring the invalidator is ALSO what installs it, by wrapping this service's
// repo handles (invalidating_repo.go). It used to be a call each mutating
// method had to remember and 15 of 17 forgot — among them SetEnabled, so a
// node the operator disabled kept being served for up to a minute, and
// UpdateSeparator, so a disabled separator kept rendering while the admin UI
// reported success. Making it a property of the WRITE means a new mutating
// method cannot forget.
//
// Done here rather than in New so a Service assembled by struct literal — as
// every test in this package does — behaves the same as the wired one the
// moment it declares that it wants invalidation. Doing it in New would leave
// those silently un-invalidating, which is the exact failure mode being fixed.
//
// Only THIS service's handles are wrapped. The traffic poll and health loop
// keep the undecorated repos they were given, so their per-cycle column writes
// do not keep the render cache empty.
func (s *Service) SetSubscriptionInvalidator(invalidate func()) {
	s.invalidateSubscriptions = invalidate
	if s.repoWrapped {
		// Idempotent: re-wiring must not stack a second wrapper, which would
		// fire the same drop twice per write.
		return
	}
	s.repoWrapped = true
	if s.nodes != nil {
		s.nodes = invalidatingNodeRepo{NodeRepo: s.nodes, notify: s.invalidateSubscriptionCaches}
	}
	if s.separators != nil {
		s.separators = invalidatingSeparatorRepo{SeparatorRepo: s.separators, notify: s.invalidateSubscriptionCaches}
	}
}

func (s *Service) invalidateSubscriptionCaches() {
	if s.invalidateSubscriptions != nil {
		s.invalidateSubscriptions()
	}
}

// markConfigSyncGaveUp moves a node off ConfigSyncPending once its retry has
// been cancelled, because Pending is a promise: the admin dot reads "配置下发
// 待重试" / "Config push pending retry" and the Sync Tasks view is where a
// retry would be visible. Leaving the row Pending after the last attempt was
// cancelled left that promise standing forever — the state was honest that the
// push had failed and dishonest about what would happen next.
//
// Only the config-carrying task types touch the column. A SetEnabled task that
// gives up is a real problem too, but it is not a CONFIG sync failure and
// mislabelling it would make the dot mean two different things.
//
// Best-effort: this runs after the task was already cancelled, so a failed
// write here must not stall the rest of the due batch. It is logged at Warn
// because the consequence — a row still claiming a retry — is exactly what
// this function exists to prevent.
func (s *Service) markConfigSyncGaveUp(ctx context.Context, task *domain.SyncTask, cause error) {
	if task.Type != domain.SyncTaskNodeUpdate && task.Type != domain.SyncTaskNodeCreate {
		return
	}
	n, err := s.nodes.GetByID(ctx, task.TargetID)
	if err != nil {
		log.Warn("config-sync give-up: node not readable",
			"node_id", task.TargetID, "err", err)
		return
	}
	if n.ConfigSyncState == domain.ConfigSyncFailed {
		return
	}
	n.SetConfigSyncState(domain.ConfigSyncFailed, time.Now())
	if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
		log.Warn("config-sync give-up: state not written; the node will keep claiming a pending retry",
			"node_id", n.ID, "err", err)
		return
	}
	log.Warn("config push gave up; node left un-converged and rendering from the panel's own config",
		"node_id", n.ID, "task_id", task.ID, "cause", cause.Error())
}

// runBackground routes fire-and-forget work through the tracked dispatcher when
// wired (drained by Shutdown, cancellable ctx), else an untracked safego.Go.
func (s *Service) runBackground(name string, fn func(ctx context.Context)) {
	if s.bg != nil {
		s.bg(name, fn)
		return
	}
	safego.Go(name, func() { fn(context.Background()) })
}

func New(nodes ports.NodeRepo, separators ports.SeparatorRepo, pool ports.XUIPool, cleaner InboundCleaner,
	tasks ports.SyncTaskRepo, groups ports.GroupRepo, users ports.UserRepo) *Service {
	return &Service{
		nodes:      nodes,
		separators: separators,
		pool:       pool,
		cleaner:    cleaner,
		tasks:      tasks,
		groups:     groups,
		users:      users,
	}
}

// ---- Read ----

func (s *Service) Get(ctx context.Context, id int64) (*domain.Node, error) {
	return s.nodes.GetByID(ctx, id)
}

// GetInboundConfig returns the inbound's connection config for the admin edit
// dialog. Serve captured PSP-owned intent even while synchronization is pending,
// failed or drifting: an edit must neither adopt upstream drift nor depend on an
// online agent after migration/reinstallation. This read does not establish
// runtime readiness; rendering retains its stricter convergence guard. Fall back
// to a live fetch only for never-captured nodes. The detail page's client list comes
// from ListClientsOfInbound (always live), so stripping clients here is fine.
func (s *Service) GetInboundConfig(ctx context.Context, id int64) (*ports.Inbound, error) {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if inboundcfg.HasStoredConfig(n) {
		return inboundcfg.InboundFromNode(n), nil
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, err
	}
	return c.GetInbound(ctx, n.InboundID)
}

// ScanRealityTargets proxies discovery to the explicitly selected 3X-UI panel.
// PSP must never probe the target itself: a central PSP host can have different
// routing, filtering, DNS, and latency than the Xray node that will actually use
// the REALITY target. 3X-UI >= 3.4.2 implements the optional RealityScanner
// capability; PSP 3.9.1 raises its compatibility floor to that version.
func (s *Service) ScanRealityTargets(ctx context.Context, panelID int64, targets string) ([]ports.RealityScanResult, error) {
	if panelID <= 0 {
		return nil, fmt.Errorf("%w: panel_id is required", domain.ErrValidation)
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return nil, err
	}
	scanner, ok := c.(ports.RealityScanner)
	if !ok {
		return nil, fmt.Errorf("%w: %w", domain.ErrValidation, ports.ErrXUIEndpointUnsupported)
	}
	return scanner.ScanRealityTargets(ctx, targets)
}

// List returns the nodes an administrator manages: the hidden listeners of
// tunnel lines are reached through the lines of their landing.
func (s *Service) List(ctx context.Context) ([]*domain.Node, error) {
	all, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, n := range all {
		if !n.IsTunnelChild() {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *Service) ListPaged(ctx context.Context, p ports.Pagination) ([]*domain.Node, int64, error) {
	return s.nodes.ListPaged(ctx, p)
}

// ---- Create flows ----

// ---- Separator CRUD --------------------------------------------------------
//
// Separators live in their own `nodes_separator` table (see
// internal/adapters/sqlstore/separator_repo.go) and are bound to groups by an
// explicit list, not by tag_filter. The pre-v3.0.0-beta.7 design that
// stashed them in `nodes` with kind='separator' + synthetic negative
// inbound_id is gone — V4 validates that V3 already migrated those rows.
//
// These methods stay on node.Service rather than in their own package
// because the operations are trivial pass-throughs to SeparatorRepo and
// admin UI surfaces them on the same Nodes page anyway. Promote to a
// dedicated package if non-trivial business logic ever attaches.

func (s *Service) ListSeparators(ctx context.Context) ([]*domain.SeparatorEntry, error) {
	if s.separators == nil {
		return nil, nil
	}
	return s.separators.List(ctx)
}

func (s *Service) ListSeparatorsPaged(ctx context.Context, p ports.Pagination) ([]*domain.SeparatorEntry, int64, error) {
	if s.separators == nil {
		return nil, 0, nil
	}
	return s.separators.ListPaged(ctx, p)
}

func (s *Service) CreateSeparator(ctx context.Context, e *domain.SeparatorEntry) error {
	if e == nil || strings.TrimSpace(e.DisplayName) == "" {
		return fmt.Errorf("%w: display_name is required", domain.ErrValidation)
	}
	if s.separators == nil {
		return fmt.Errorf("separator repo not configured")
	}
	e.DisplayName = strings.TrimSpace(e.DisplayName)
	// SortOrder <= 0 means "place at tail" — the admin UI no longer asks
	// for the value because the drag-to-reorder flow re-numbers everything
	// in 10-step increments anyway. Compute max across BOTH nodes and
	// separators (they share one ordering scale) and add 10.
	if e.SortOrder <= 0 {
		next, err := s.nextSortOrder(ctx)
		if err != nil {
			return err
		}
		e.SortOrder = next
	}
	return s.separators.Create(ctx, e)
}

func (s *Service) UpdateSeparator(ctx context.Context, e *domain.SeparatorEntry) error {
	if e == nil || strings.TrimSpace(e.DisplayName) == "" {
		return fmt.Errorf("%w: display_name is required", domain.ErrValidation)
	}
	if s.separators == nil {
		return fmt.Errorf("separator repo not configured")
	}
	e.DisplayName = strings.TrimSpace(e.DisplayName)
	// The caller builds this entry from a request DTO, so the fields the form
	// does not carry arrive as zero values. Two of them must come from storage
	// instead, and for different reasons:
	//
	//   SortOrder is WRITTEN, so a zero would clobber the position the admin
	//     set by dragging (the edit dialog no longer shows the field).
	//   CreatedAt is NOT written — the repo omits the column — but it is
	//     returned to the caller, which renders it and updates its local row
	//     from the response. Leaving it zero made the API report a creation
	//     date of year 1 for a row whose stored value was fine: the write was
	//     honest and the answer was not.
	//
	// So the entry is reconciled with storage before the write, and the caller
	// gets back what is actually stored rather than what it sent.
	existing, err := s.separators.GetByID(ctx, e.ID)
	if err != nil {
		return err
	}
	if e.SortOrder <= 0 {
		e.SortOrder = existing.SortOrder
	}
	e.CreatedAt = existing.CreatedAt
	return s.separators.Update(ctx, e)
}

// nextSortOrder returns max(sort_order across nodes, separators and the
// positions of relay lines that exist) + 10, or 10 when there is none of them.
// Used to drop new separators at the tail of the merged list without forcing
// the admin to pick a number. Position rows whose line or node is gone are not
// counted; nodeRepo.Create applies the same rule (maxLivePosition).
func (s *Service) nextSortOrder(ctx context.Context) (int, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return 0, err
	}
	seps, err := s.separators.List(ctx)
	if err != nil {
		return 0, err
	}
	maxSort := 0
	for _, n := range nodes {
		if n.SortOrder > maxSort {
			maxSort = n.SortOrder
		}
		// A line dragged to the bottom sits past every node; a separator
		// appended below nodes alone would land above it.
		for _, line := range n.Relays {
			if line.Position != nil && *line.Position > maxSort {
				maxSort = *line.Position
			}
		}
	}
	for _, sep := range seps {
		if sep.SortOrder > maxSort {
			maxSort = sep.SortOrder
		}
	}
	return maxSort + 10, nil
}

func (s *Service) DeleteSeparator(ctx context.Context, id int64) error {
	if s.separators == nil {
		return fmt.Errorf("separator repo not configured")
	}
	return s.separators.Delete(ctx, id)
}

// ReorderSeparators rewrites sort_order for every listed separator in
// one transaction. Sibling of Reorder() for the nodes table — the
// frontend issues two PUTs (one per kind) when the admin drags rows
// in the mixed table. Validation parallels Reorder: non-empty +
// duplicate IDs rejected + positive IDs only.
func (s *Service) ReorderSeparators(ctx context.Context, updates []ports.SeparatorSortUpdate) error {
	if s.separators == nil {
		return fmt.Errorf("separator repo not configured")
	}
	if len(updates) == 0 {
		return fmt.Errorf("%w: no updates", domain.ErrValidation)
	}
	seen := make(map[int64]struct{}, len(updates))
	for _, u := range updates {
		if u.SeparatorID <= 0 {
			return fmt.Errorf("%w: separator_id must be positive", domain.ErrValidation)
		}
		if _, dup := seen[u.SeparatorID]; dup {
			return fmt.Errorf("%w: duplicate separator_id %d", domain.ErrValidation, u.SeparatorID)
		}
		seen[u.SeparatorID] = struct{}{}
	}
	// Invalidation rides the repo write (invalidating_repo.go), so no explicit
	// call here — it would fire the same drop twice.
	return s.separators.BatchUpdateSortOrder(ctx, updates)
}

// ImportExisting registers an inbound that already lives in 3X-UI under
// panel management. No 3X-UI inbound-level write happens; only metadata is
// persisted, and clients are synced for any matching groups so newly
// added users immediately see this node in their subscriptions.
func (s *Service) ImportExisting(ctx context.Context, n *domain.Node) error {
	if n.DisplayName == "" || n.Region == "" {
		return fmt.Errorf("%w: display_name and region required", domain.ErrValidation)
	}
	defer s.lockFronts(relayPanels(n)...)()
	if err := domain.ReconcileRelayIDs(nil, n.Relays); err != nil {
		return err
	}
	// The same normalisation as the editor's save: an imported line's source
	// addresses are held to the same rules (host bits, IPv4-mapped, too broad).
	if err := resolveSourceCIDRs(nil, n); err != nil {
		return err
	}
	domain.ResolveRelayLabels(nil, n.Relays, n.Region, n.Tags)
	if err := s.validateManagedRelays(ctx, n); err != nil {
		return err
	}
	if existing, err := s.nodes.GetByPanelInbound(ctx, n.PanelID, n.InboundID); err == nil && existing != nil {
		return domain.ErrAlreadyExists
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return err
	}
	inb, err := c.GetInbound(ctx, n.InboundID)
	if err != nil {
		return fmt.Errorf("inbound %d not found on panel %d: %w", n.InboundID, n.PanelID, err)
	}
	n.Enabled = true
	// Explicit import/adoption is the one operation allowed to initialize
	// desired endpoint intent from live state. Ordinary Capture calls only
	// update the observed axis.
	inboundcfg.Adopt(n, inb)
	// Adopt the inbound's own VLESS flow when the admin left the field blank.
	//
	// Flow otherwise arrives ONLY from the import form, and a blank one on a
	// Reality/Vision inbound is the state every reader disagrees about: the
	// legacy push falls back to the panel's flow (reconcile.resolveFlow), render
	// emits Node.Flow and so hands the user a link with no flow, and the shared
	// path has no fallback at all — clientplan derives from Node.Flow alone, so
	// the client is PROVISIONED flowless, which is the same regression
	// resolveFlow's comment records as fixed for the legacy path.
	//
	// Importing is taking ownership of what is already there, so the honest
	// default is the value already there. Only a blank is filled: a flow the
	// admin typed is their decision and stays untouched.
	if n.Flow == "" && strings.EqualFold(n.DesiredProtocol, string(domain.ProtoVLESS)) {
		if settings, perr := xrayspec.ParseSettings(inb.Settings); perr == nil {
			n.Flow = xrayspec.FirstClientFlow(settings.Clients)
		} else {
			// Non-fatal: an unparseable settings blob costs the flow default,
			// not the import. reconcile's flow_render_divergence still reports
			// the resulting blank.
			log.Warn("import: could not parse inbound settings for flow default",
				"panel_id", n.PanelID, "inbound_id", n.InboundID, "err", perr)
		}
	}
	// The factor rules are judged against the node as it is now stored: the
	// transport of the adopted inbound decides whether its listener can guard.
	if err := s.checkSourceGuard(ctx, nil, n); err != nil {
		return err
	}
	// The CDN lines are judged against the node as it is now stored, flow included.
	if err := settleCDN(nil, n, n.Relays); err != nil {
		return err
	}
	// Import = take ownership: capture the live inbound's config into the local
	// snapshot so render reads it without a live fetch and reconcile can keep
	// 3X-UI aligned to PSP. clients[] is stripped (ownership-managed).
	offer, err := s.checkInboundAgainstFronts(ctx, n)
	if err != nil {
		return err
	}
	if err := s.nodes.Create(ctx, n); err != nil {
		return err
	}
	if err := s.finishFront(ctx, n, offer); err != nil {
		return err
	}
	s.inboundsChanged(ctx, n.PanelID)
	s.syncExistingUsersToNodeInBackground(n)
	return nil
}

// CreateInbound creates a brand new inbound in 3X-UI and registers it,
// then syncs clients for every panel user whose group would include
// this node. Admin doesn't need a separate "resync everyone" step.
func (s *Service) CreateInbound(ctx context.Context, n *domain.Node, spec ports.InboundSpec) error {
	if n.DisplayName == "" || n.Region == "" || n.PanelID == 0 {
		return fmt.Errorf("%w: display_name, region and panel_id required", domain.ErrValidation)
	}
	// A tunnel line needs its landing to exist: it is added to a saved node.
	for _, line := range n.Relays {
		if line.Tunnel() {
			return fmt.Errorf("%w: a tunnel line is added to a node that already exists", domain.ErrValidation)
		}
	}
	// Resolve before any queued retry captures the node: the request-only
	// markers are not serialized into the task payload.
	// The server's lock comes before anything reads fronts, relay lines included;
	// the keys are taken in sorted order everywhere, so locks never cross.
	defer s.lockFronts(relayPanels(n)...)()
	return s.createInboundLocked(ctx, n, spec)
}

// createInboundLocked is CreateInbound for a caller that already holds the
// locks of the node's server and of the entry servers of its lines.
func (s *Service) createInboundLocked(ctx context.Context, n *domain.Node, spec ports.InboundSpec) error {
	if !n.IsTunnelChild() {
		if err := domain.ReconcileRelayIDs(nil, n.Relays); err != nil {
			return err
		}
		if err := resolveSourceCIDRs(nil, n); err != nil {
			return err
		}
	}
	domain.ResolveRelayLabels(nil, n.Relays, n.Region, n.Tags)
	if err := s.validateManagedRelays(ctx, n); err != nil {
		return err
	}
	// The node is checked as the spec will make it: its port and transport are
	// the spec's.
	probe := *n
	inboundcfg.ApplySpec(&probe, spec)
	if err := s.checkSourceGuard(ctx, nil, &probe); err != nil {
		return err
	}
	if err := settleCDN(nil, &probe, n.Relays); err != nil {
		return err
	}
	offer, err := s.checkInboundAgainstFronts(ctx, &probe)
	if err != nil {
		return err
	}
	if err := s.persistInbound(ctx, n, spec, offer == nil && !n.IsTunnelChild(), true); err != nil || n.ID == 0 {
		return err
	}
	if err := s.finishFront(ctx, n, offer); err != nil {
		return err
	}
	s.inboundsChanged(ctx, n.PanelID)
	if !n.IsTunnelChild() {
		// A tunnel listener belongs to no group: its users are its landing's, and
		// the save of the line re-plans them.
		s.syncExistingUsersToNodeInBackground(n)
	}
	return nil
}

// persistInbound writes an inbound and its snapshot. The caller does the port
// checks and applies any front changes before notifying provisioners.
func (s *Service) persistInbound(ctx context.Context, n *domain.Node, spec ports.InboundSpec, allowQueue, enabled bool) error {
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		if !allowQueue {
			return fmt.Errorf("xui panel: %w", err) // a join is never queued: the admin retries
		}
		return s.enqueueNodeCreateTask(ctx, n, spec, fmt.Errorf("xui panel: %w", err))
	}
	inboundID, err := c.AddInbound(ctx, spec)
	if err != nil {
		if permanentErr := permanentInboundCreateError(err); permanentErr != nil {
			return permanentErr
		}
		if !allowQueue {
			// A queued create would run later with no join: the node would land on
			// the shared port beside the front. Return the error instead.
			return fmt.Errorf("xui addInbound: %w", err)
		}
		return s.enqueueNodeCreateTask(ctx, n, spec, fmt.Errorf("xui addInbound: %w", err))
	}
	n.InboundID = inboundID
	n.Enabled = enabled
	// v3.5 write-through: persist the just-pushed config into the local snapshot
	// so the node renders without a live fetch from its first subscription.
	inboundcfg.ApplySpec(n, spec)
	if async, ok := c.(ports.AsynchronousApplier); ok && async.ApplyIsAsynchronous() {
		n.SetConfigSyncState(domain.ConfigSyncPending, time.Now())
	}
	if err := s.nodes.Create(ctx, n); err != nil {
		_ = c.DelInbound(context.Background(), inboundID)
		return err
	}
	return nil
}

// RecreateInboundOnServer rebuilds a node's inbound on its CURRENT panel from PSP's
// captured config snapshot, then relinks the node to the newly-created inbound id.
//
// Use case: the node's Server was repointed to a fresh/empty 3X-UI (shows
// "Connected (0)"). PSP attaches clients to EXISTING inbounds by id, so an empty
// server has nothing for the node to use — and PSP never creates inbounds on its own.
// But PSP OWNS the inbound config (the v3.5 snapshot), so this pushes it back as a new
// inbound instead of the admin re-creating it by hand. Clients follow:
// provisionNodeMembersInBackground re-provisions each eligible member's shared
// client onto the new inbound.
//
// On a NATIVE panel (ports.AsynchronousApplier) PSP owns the inbound outright, so
// this never rebuilds or relinks anything: it only re-provisions the members.
//
// Idempotent: it re-creates the inbound only when it is MISSING (otherwise it leaves
// the live one and just re-provisions clients), so it never duplicates an inbound and
// doubles as a manual "push clients now". Recreating needs a captured config
// (HasLocalConfig); a persist failure rolls back the just-created inbound. A full-row
// Update is fine here: the node was unreachable (no live traffic/health writes to clobber).
func (s *Service) RecreateInboundOnServer(ctx context.Context, nodeID int64) error {
	n, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	if n.IsSeparator() {
		return fmt.Errorf("%w: node %d is a separator", domain.ErrValidation, nodeID)
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return err
	}
	// A native panel's inbound is PSP's own desired config; the agent deploys it
	// and GetInbound only reads the agent's last full report. A report that lacks
	// the node (not reported yet, agent offline, stale) therefore says nothing
	// about the inbound being gone, and treating it so would burn a new
	// compatibility inbound id and relink the node for nothing.
	// What Recreate still means here is "push the members' clients now". It does
	// NOT re-enable a disabled node: enabling is SetEnabled's job.
	if async, ok := c.(ports.AsynchronousApplier); ok && async.ApplyIsAsynchronous() {
		log.Info("recreate: native panel owns the inbound — re-provisioning members only", "node_id", nodeID, "panel_id", n.PanelID, "inbound_id", n.InboundID)
		s.provisionNodeMembersInBackground(n)
		return nil
	}
	// IDEMPOTENT: if the inbound is MISSING on the panel, recreate it from the captured
	// snapshot + relink the node; if it's already there, skip creation. EITHER way the
	// members' clients are (re-)provisioned below — so re-clicking this on a node whose
	// inbound already exists but whose clients didn't push (e.g. the panel was briefly
	// broken so the provision queue gave up) is a safe "push clients now".
	if inb, gerr := c.GetInbound(ctx, n.InboundID); gerr != nil || inb == nil {
		if !inboundcfg.HasLocalConfig(n) {
			return fmt.Errorf("%w: node %d has no captured inbound config to recreate", domain.ErrValidation, nodeID)
		}
		spec, serr := inboundcfg.SpecFromNode(n)
		if serr != nil {
			return fmt.Errorf("recreate inbound on panel %d: %w", n.PanelID, serr)
		}
		spec.Enable = true
		newID, aerr := c.AddInbound(ctx, spec)
		if aerr != nil {
			return fmt.Errorf("recreate inbound on panel %d: %w", n.PanelID, aerr)
		}
		n.InboundID = newID
		n.Enabled = true
		inboundcfg.ApplySpec(n, spec) // re-stamp synced: we just pushed the snapshot live
		if uerr := s.nodes.Update(ctx, n); uerr != nil {
			_ = c.DelInbound(context.Background(), newID) // roll back the orphan inbound
			return fmt.Errorf("relink node %d to new inbound %d: %w", nodeID, newID, uerr)
		}
		log.Info("recreated node inbound on its server", "node_id", nodeID, "panel_id", n.PanelID, "new_inbound_id", newID)
	} else if inboundcfg.HasLocalConfig(n) {
		// Inbound already there → re-push the captured snapshot to HEAL it in place.
		// An inbound created before the clients[] fix carries clients-less settings,
		// which makes 3X-UI blank-200 every /clients/add (notably shadowsocks); the
		// UpdateInbound RMW re-applies the snapshot (ensureClientsArray guarantees the
		// clients[] array) while preserving whatever clients are live. So re-clicking
		// recreate fixes an existing un-addable inbound without delete+recreate.
		spec, serr := inboundcfg.SpecFromNode(n)
		if serr != nil {
			return fmt.Errorf("heal existing inbound %d on panel %d: %w", n.InboundID, n.PanelID, serr)
		}
		if uerr := c.UpdateInbound(ctx, n.InboundID, spec); uerr != nil {
			return fmt.Errorf("heal existing inbound %d on panel %d: %w", n.InboundID, n.PanelID, uerr)
		}
		log.Info("recreate: re-pushed snapshot to heal existing inbound", "node_id", nodeID, "panel_id", n.PanelID, "inbound_id", n.InboundID)
	} else {
		log.Info("recreate: inbound present, no captured snapshot to re-push — provisioning clients only", "node_id", nodeID, "panel_id", n.PanelID, "inbound_id", n.InboundID)
	}
	// (Re-)provision the members' clients onto the node's inbound, off the request thread —
	// button returns fast (no 30s HTTP timeout on a populous node); clients appear within
	// seconds, not on the next sync-task tick. Per member, ResyncMembershipOrEnqueue
	// provisions live and, IF that fails, drops the member into the sync-task queue for
	// retry (so a flaky 3X-UI call never loses the client).
	s.provisionNodeMembersInBackground(n)
	return nil
}

// provisionNodeMembersInBackground runs provisionNodeMembers off the request
// thread, through the tracked dispatcher so App.Shutdown drains it.
func (s *Service) provisionNodeMembersInBackground(n *domain.Node) {
	snap := *n
	s.runBackground("node.recreate-provision-members", func(ctx context.Context) {
		s.provisionNodeMembers(ctx, &snap)
	})
}

// provisionNodeMembers re-provisions every account-active member of the groups that include
// n, attaching their shared client to n's inbound. Immediate per member, with a
// sync-task-queue fallback on failure (via ResyncMembershipOrEnqueue). Synchronous +
// testable; the caller runs it in the background.
func (s *Service) provisionNodeMembers(ctx context.Context, n *domain.Node) {
	if s.resyncer == nil || s.groups == nil || s.users == nil {
		return
	}
	groups, err := s.groups.List(ctx)
	if err != nil {
		log.Warn("recreate: list groups for member provision", "node_id", n.ID, "err", err)
		return
	}
	// Collect every account-active member of a matching group, deduped and
	// order-preserving. AccountLoginAllowed includes historical expiry/quota
	// encodings; their client is still provisioned but EffectiveEnabled keeps it
	// disabled until the service restriction clears.
	seen := make(map[int64]bool)
	var memberIDs []int64
	for _, g := range groups {
		if !s.groupIncludes(ctx, n, g) {
			continue
		}
		members, err := s.users.ListByGroup(ctx, g.ID)
		if err != nil {
			log.Warn("recreate: list members", "group_id", g.ID, "err", err)
			continue
		}
		for _, u := range members {
			if !domain.AccountLoginAllowed(u.Enabled, u.AutoDisabledReason) || seen[u.ID] {
				continue
			}
			seen[u.ID] = true
			memberIDs = append(memberIDs, u.ID)
		}
	}
	if len(memberIDs) == 0 {
		return
	}
	// Warm-up: front-load every member's create onto n's inbound in ONE bulkCreate +
	// bulkAttach (one Xray restart) before the per-member resyncs. Best-effort — on
	// failure the authoritative per-member pass below still provisions each one.
	if err := s.resyncer.BulkProvisionNodeMembers(ctx, n, memberIDs); err != nil {
		log.Warn("recreate: bulk warm node members", "node_id", n.ID, "members", len(memberIDs), "err", err)
	}
	// Authoritative per-member pass: confirm + mark provisioned + push lifecycle +
	// reconcile orphans. After the warm-up the create is a no-op (client already
	// present), so this no longer costs a per-member Xray restart for the create.
	provisioned := 0
	for _, uid := range memberIDs {
		if err := s.resyncer.ResyncMembershipOrEnqueue(ctx, uid, "provision client on recreated node "+n.DisplayName); err != nil {
			log.Warn("recreate: provision member", "node_id", n.ID, "user_id", uid, "err", err)
			continue
		}
		provisioned++
	}
	log.Info("recreate: provisioned clients for node members", "node_id", n.ID, "members", provisioned, "warmed", len(memberIDs))
}

// ---- Update flows ----

// Reorder rewrites sort_order for every (node_id, sort_order) pair in one
// transaction. Drives the admin drag-to-reorder UI: the client renumbers the
// visible list in 10-step increments and POSTs the result back here. Empty
// input, non-positive ids, or a duplicate id all reject the whole batch — a
// partial reorder would leave the list in a confusing half-state.
func (s *Service) Reorder(ctx context.Context, updates []ports.NodeSortUpdate) error {
	if len(updates) == 0 {
		return fmt.Errorf("%w: no updates", domain.ErrValidation)
	}
	seen := make(map[int64]struct{}, len(updates))
	for _, u := range updates {
		if u.NodeID <= 0 {
			return fmt.Errorf("%w: node_id must be positive", domain.ErrValidation)
		}
		if _, dup := seen[u.NodeID]; dup {
			return fmt.Errorf("%w: duplicate node_id %d", domain.ErrValidation, u.NodeID)
		}
		seen[u.NodeID] = struct{}{}
	}
	// Invalidation rides the repo write — see ReorderSeparators.
	return s.nodes.BatchUpdateSortOrder(ctx, updates)
}

func (s *Service) UpdateMetadata(ctx context.Context, n *domain.Node) error {
	return s.updateMetadata(ctx, n, nil)
}

// updateMetadata is UpdateMetadata. switching names the lines whose mode this
// save may change: only SwitchLineMode, which confirms it, passes any.
func (s *Service) updateMetadata(ctx context.Context, n *domain.Node, switching map[string]bool) error {
	// The tunnels only a removed listener's routes used are cleaned up once the
	// server locks below are let go (deferred first, so it runs last).
	ctx, sink := withRouteSink(ctx)
	defer s.cleanup(ctx, sink)
	previous, unlock, err := s.lockAndLoad(ctx, n.ID, n.Relays)
	if err != nil {
		return err
	}
	defer unlock()
	if previous.IsTunnelChild() {
		return s.updateTunnelListenerMetadata(ctx, previous, n)
	}
	return s.updateMetadataLocked(ctx, previous, n, switching)
}

// lockAndLoad takes the locks of a landing's server and of the entry server of
// each of its lines (stored and about to be stored), and only then reads the
// stored node, so that what a save plans from is what no other save can change
// under it. A concurrent save that moved a line to another entry server between
// the first read and the locks is caught by comparing the lock set with the
// re-read node, and the load is retried with the wider set.
func (s *Service) lockAndLoad(ctx context.Context, id int64, next []domain.RelayLine) (*domain.Node, func(), error) {
	panels := map[int64]bool{}
	for _, line := range next {
		panels[line.EntryPanelID] = true
	}
	for attempt := 0; ; attempt++ {
		peek, err := s.nodes.GetByID(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		panels[peek.PanelID] = true
		for _, line := range peek.Relays {
			panels[line.EntryPanelID] = true
		}
		list := make([]int64, 0, len(panels))
		for p := range panels {
			list = append(list, p)
		}
		unlock := s.lockFronts(list...)
		previous, err := s.nodes.GetByID(ctx, id)
		if err != nil {
			unlock()
			return nil, nil, err
		}
		covered := panels[previous.PanelID]
		for _, line := range previous.Relays {
			covered = covered && panels[line.EntryPanelID]
		}
		if covered || attempt >= 3 {
			if !covered {
				unlock()
				return nil, nil, fmt.Errorf("%w: the node's lines changed while it was being saved: reload it and try again", domain.ErrConflict)
			}
			return previous, unlock, nil
		}
		unlock()
		for _, line := range previous.Relays {
			panels[line.EntryPanelID] = true
		}
	}
}

// updateMetadataLocked is the save of a landing's metadata for a caller that
// holds lockAndLoad's locks and passes the node as it read it under them.
func (s *Service) updateMetadataLocked(ctx context.Context, previous, n *domain.Node, switching map[string]bool) error {
	if err := domain.ReconcileRelayIDs(previous.Relays, n.Relays); err != nil {
		return err
	}
	if err := resolveLineFactors(previous, n); err != nil {
		return err
	}
	if err := resolveSourceCIDRs(previous, n); err != nil {
		return err
	}
	checked := *previous
	checked.Relays, checked.TrafficFactorMilli = n.Relays, n.TrafficFactorMilli
	if err := s.checkSourceGuard(ctx, previous, &checked); err != nil {
		return err
	}
	// Omitted line labels keep their saved value; new lines take the node's
	// value after this save. Lines never follow later node edits (ADR 0040).
	domain.ResolveRelayLabels(previous.Relays, n.Relays, n.Region, n.Tags)
	// Check CDN lines against the saved transport and the flow in this request
	// before planning any listeners.
	saved := *previous
	saved.Flow = n.Flow
	if err := settleCDN(previous.Relays, &saved, n.Relays); err != nil {
		return err
	}
	// Tunnel lines: the server-owned fields come from the stored lines, and the
	// listeners to make and to delete are settled before anything is written.
	work, err := s.planTunnels(ctx, previous, n, switching)
	if err != nil {
		return err
	}
	// The landing's own panel and port decide collisions, and metadata edits
	// do not change them, so check against the stored node. The listeners about
	// to be removed do not count: a tunnel line going back to a forward may take
	// its listener's port.
	landing := *previous
	landing.Relays = n.Relays
	if err := s.validateManagedRelays(ctx, &landing, work.dropping...); err != nil {
		return err
	}
	if err := s.validateEgress(previous, n.EgressPanelID); err != nil {
		return err
	}
	// Only now is the confirmation asked: a save that would be refused anyway is
	// refused first.
	if work.refusal != nil {
		return work.refusal
	}
	// A line switched off or on moves its listener the same way: settle what
	// that does to a shared port before anything is written.
	enabledCheck := *previous
	enabledCheck.Relays, enabledCheck.Enabled = n.Relays, previous.Enabled
	if err := s.tunnelEnabledRefusal(ctx, &enabledCheck); err != nil {
		return err
	}
	if err := s.makeTunnels(ctx, previous, n, work); err != nil {
		return err
	}
	// The listeners of lines that go are removed BEFORE the lines are saved: a cut
	// short save then leaves a line whose listener is missing (withheld, and
	// fixed by removing or switching the line), never a hidden listener that no
	// line owns.
	if err := s.dropTunnels(ctx, work); err != nil {
		s.undoTunnels(ctx, work)
		return err
	}
	// Column-scoped: a full-row Update here would roll back the poll-owned
	// columns (traffic counters / health / inbound-config snapshot) to the
	// stale values the edit dialog loaded.
	if err := s.nodes.UpdateMetadata(ctx, n); err != nil {
		s.undoTunnels(ctx, work)
		return err
	}
	// The listeners follow their lines: enabled only while the line and the
	// landing are, and carrying the line's labels, address and factor.
	updated := *previous
	updated.Relays = n.Relays
	updated.DisplayName = n.DisplayName // the position stays the stored one: a metadata save never moves a node
	if err := s.followTunnelLines(ctx, &updated); err != nil {
		return err
	}
	// Each line a user can see has a client of its own (ADR 0043): a line that
	// appears, disappears or changes who may see it re-plans the members.
	if domain.LineSetChanged(previous.Relays, n.Relays) {
		s.provisionNodeMembersInBackground(&updated)
	}
	return nil
}

// updateTunnelListenerMetadata is a metadata save of a tunnel listener, which
// is edited as a node: only its flow is its own. Its name, address, labels,
// exit and traffic factor follow its line.
func (s *Service) updateTunnelListenerMetadata(ctx context.Context, previous, n *domain.Node) error {
	next := *previous
	next.Flow = n.Flow
	return s.nodes.UpdateMetadata(ctx, &next)
}

// resolveLineFactors validates the edited line factors and keeps the stored
// one wherever the edit carries none: an editor that does not know about
// factors must not reset them. A new line without one is one-for-one.
func resolveLineFactors(previous, n *domain.Node) error {
	if n.TrafficFactorMilli == nil {
		n.TrafficFactorMilli = previous.TrafficFactorMilli
	}
	if n.TrafficFactorMilli != nil {
		if err := domain.ValidateTrafficFactor(*n.TrafficFactorMilli); err != nil {
			return err
		}
	}
	saved := make(map[string]*int, len(previous.Relays))
	for _, r := range previous.Relays {
		saved[r.ID] = r.TrafficFactorMilli
	}
	for i := range n.Relays {
		r := &n.Relays[i]
		if r.TrafficFactorMilli == nil {
			r.TrafficFactorMilli = saved[r.ID]
		}
		if r.TrafficFactorMilli != nil {
			if err := domain.ValidateTrafficFactor(*r.TrafficFactorMilli); err != nil {
				return err
			}
		}
	}
	return nil
}

// LineFactorChange sets one line's traffic factor.
type LineFactorChange struct {
	Line        domain.LineKey
	FactorMilli int
}

// AppliedLineFactor is a change that took effect, for the audit log.
type AppliedLineFactor struct {
	Line     domain.LineKey
	NodeName string
	From, To int
}

// SetLineFactors sets the factors of lines on one server in one pass (the
// server page's bulk edit). Every line must belong to a node of panelID; the
// whole request is refused before anything is written otherwise. Only the
// factor changes: each node is written through the column-scoped metadata
// writer with everything else as stored.
func (s *Service) SetLineFactors(ctx context.Context, panelID int64, changes []LineFactorChange) ([]AppliedLineFactor, error) {
	// The landings are read, written and their tunnel listeners brought in step
	// under the locks of the server and of the entry servers of their tunnel
	// lines, as every other write of these lines is. The set of entry servers is
	// learned from a first read and checked again under the locks.
	panels := map[int64]bool{panelID: true}
	for attempt := 0; ; attempt++ {
		for _, c := range changes {
			if n, err := s.nodes.GetByID(ctx, c.Line.NodeID); err == nil {
				for _, p := range tunnelEntryPanels(n) {
					panels[p] = true
				}
			}
		}
		list := make([]int64, 0, len(panels))
		for p := range panels {
			list = append(list, p)
		}
		unlock := s.lockFronts(list...)
		covered := true
		for _, c := range changes {
			if n, err := s.nodes.GetByID(ctx, c.Line.NodeID); err == nil {
				for _, p := range tunnelEntryPanels(n) {
					covered = covered && panels[p]
				}
			}
		}
		if covered {
			defer unlock()
			break
		}
		unlock()
		if attempt >= 3 {
			return nil, fmt.Errorf("%w: the tunnel lines changed while the factors were being saved: reload and try again", domain.ErrConflict)
		}
	}
	return s.setLineFactorsLocked(ctx, panelID, changes)
}

func (s *Service) setLineFactorsLocked(ctx context.Context, panelID int64, changes []LineFactorChange) ([]AppliedLineFactor, error) {
	byNode := map[int64]*domain.Node{}
	original := map[int64]*domain.Node{}
	var order []int64
	seen := make(map[domain.LineKey]bool, len(changes))
	for _, c := range changes {
		if seen[c.Line] {
			return nil, fmt.Errorf("%w: line %d/%s is listed twice", domain.ErrValidation, c.Line.NodeID, c.Line.LineID)
		}
		seen[c.Line] = true
		if err := domain.ValidateTrafficFactor(c.FactorMilli); err != nil {
			return nil, err
		}
		n, ok := byNode[c.Line.NodeID]
		if !ok {
			loaded, err := s.nodes.GetByID(ctx, c.Line.NodeID)
			if err != nil {
				return nil, err
			}
			if loaded.IsTunnelChild() {
				return nil, fmt.Errorf("%w: node %d is the listener of a tunnel line: its factor is the line's, set on the line of its landing", domain.ErrValidation, loaded.ID)
			}
			if loaded.PanelID != panelID || loaded.IsSeparator() {
				return nil, fmt.Errorf("%w: node %d is not on server %d", domain.ErrValidation, c.Line.NodeID, panelID)
			}
			n = loaded
			byNode[n.ID] = n
			saved := *loaded
			saved.Relays = append([]domain.RelayLine(nil), loaded.Relays...)
			original[n.ID] = &saved
			order = append(order, n.ID)
		}
		if !c.Line.IsDirect() && !slices.ContainsFunc(n.Relays, func(r domain.RelayLine) bool { return r.ID == c.Line.LineID }) {
			return nil, fmt.Errorf("%w: node %d has no line %q", domain.ErrValidation, n.ID, c.Line.LineID)
		}
	}
	var applied []AppliedLineFactor
	for _, c := range changes {
		n := byNode[c.Line.NodeID]
		milli := c.FactorMilli
		if c.Line.IsDirect() {
			if from := n.EffectiveTrafficFactor(); from != milli {
				applied = append(applied, AppliedLineFactor{Line: c.Line, NodeName: n.DisplayName, From: from, To: milli})
			}
			n.TrafficFactorMilli = &milli
			continue
		}
		for i := range n.Relays {
			if n.Relays[i].ID == c.Line.LineID {
				if from := n.Relays[i].EffectiveTrafficFactor(); from != milli {
					applied = append(applied, AppliedLineFactor{Line: c.Line, NodeName: n.DisplayName, From: from, To: milli})
				}
				n.Relays[i].TrafficFactorMilli = &milli
			}
		}
	}
	for _, id := range order {
		if err := s.checkSourceGuard(ctx, original[id], byNode[id]); err != nil {
			return nil, err
		}
	}
	// One write per node. A failure puts the nodes already written back the
	// way they were, so the request stays all or nothing.
	for i, id := range order {
		if err := s.nodes.UpdateMetadata(ctx, byNode[id]); err != nil {
			for _, done := range order[:i] {
				if rerr := s.nodes.UpdateMetadata(ctx, original[done]); rerr != nil {
					log.Error("line factors: roll back node", "node_id", done, "err", rerr)
				}
			}
			return nil, err
		}
	}
	// A tunnel line's listener carries a copy of its line's factor, which the
	// entry server's own planning reads: keep it in step.
	// A failure is returned, not only logged: the factors are saved, a listener's
	// copy is not, and the entry server grants quota through the copy. The next
	// sync also raises an issue (tunnel_factor_stale) for every listener whose
	// copy differs from its line.
	for _, id := range order {
		if err := s.followTunnelLines(ctx, byNode[id]); err != nil {
			log.Warn("line factors: a tunnel listener did not follow its line", "node_id", id, "err", err)
			return applied, fmt.Errorf("the traffic factors were saved, but a tunnel listener of node %q did not take its line's factor (retry the save): %w", byNode[id].DisplayName, err)
		}
	}
	return applied, nil
}

// ChangedLineFactors lists the lines whose factor differs between two
// versions of a node, for the audit log.
func ChangedLineFactors(before, after *domain.Node) []AppliedLineFactor {
	var out []AppliedLineFactor
	if from, to := before.EffectiveTrafficFactor(), after.EffectiveTrafficFactor(); from != to {
		out = append(out, AppliedLineFactor{Line: domain.DirectLine(after.ID), NodeName: after.DisplayName, From: from, To: to})
	}
	prev := map[string]int{}
	for _, r := range before.Relays {
		prev[r.ID] = r.EffectiveTrafficFactor()
	}
	for _, r := range after.Relays {
		from, existed := prev[r.ID]
		if !existed {
			from = domain.TrafficFactorDefault
		}
		if to := r.EffectiveTrafficFactor(); from != to {
			out = append(out, AppliedLineFactor{Line: domain.LineKey{NodeID: after.ID, LineID: r.ID}, NodeName: after.DisplayName, From: from, To: to})
		}
	}
	return out
}

func (s *Service) UpdateInboundConfig(ctx context.Context, id int64, spec ports.InboundSpec) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	// v3.5 write-through (local-first): PSP owns the inbound config, so persist
	// the new config into the local snapshot before pushing. Render reflects it
	// immediately and survives a 3X-UI outage; the push (or its retry task)
	// then aligns 3X-UI. Use the column-scoped writer so a concurrent health
	// pass doesn't clobber our snapshot — and we don't clobber its writes.
	defer s.lockFronts(n.PanelID)()
	return s.updateInboundConfigLockedNode(ctx, n, spec)
}

// updateInboundConfigLocked is UpdateInboundConfig for a caller that holds the
// node's server lock.
func (s *Service) updateInboundConfigLocked(ctx context.Context, id int64, spec ports.InboundSpec) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return s.updateInboundConfigLockedNode(ctx, n, spec)
}

func (s *Service) updateInboundConfigLockedNode(ctx context.Context, n *domain.Node, spec ports.InboundSpec) error {
	sniffingBefore := n.Sniffing
	if err := s.tunnelListenerRefusal(ctx, n, spec); err != nil {
		return err
	}
	next := *n
	inboundcfg.ApplySpec(&next, spec)
	if err := domain.ValidateCDNRelays(&next, next.Relays); err != nil {
		return fmt.Errorf("%w (the change would leave its CDN lines without a transport a CDN can carry: remove those lines first, or keep a TLS + WebSocket, gRPC or HTTPUpgrade transport)", err)
	}
	if err := s.checkListenerGuard(ctx, n, &next); err != nil {
		return err
	}
	offer, err := s.checkInboundAgainstFronts(ctx, &next)
	if err != nil {
		return err
	}
	inboundcfg.ApplySpec(n, spec)
	if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
		return err
	}
	if err := s.finishFront(ctx, n, offer); err != nil {
		return err
	}
	if n.Sniffing != sniffingBefore {
		// Whether the panel can enforce a group allowlist depends on it.
		s.inboundsChanged(ctx, n.PanelID)
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		s.markConfigPending(ctx, n)
		return s.enqueueNodeTask(ctx, domain.SyncTaskNodeUpdate, n, "update node config", spec)
	}
	if err := c.UpdateInbound(ctx, n.InboundID, spec); err != nil {
		s.markConfigPending(ctx, n)
		return s.enqueueNodeTask(ctx, domain.SyncTaskNodeUpdate, n, "update node config", spec)
	}
	if async, ok := c.(ports.AsynchronousApplier); ok && async.ApplyIsAsynchronous() {
		s.markConfigPending(ctx, n)
	}
	return nil
}

// NormalizeRealityFingerprintsForPanel idempotently converges existing desired
// snapshots whenever a trusted observation reports Xray 26.9.8+. New writes are
// normalized by the HTTP boundary; repeatedly calling this method closes boot,
// native-report, in-panel upgrade, and partial-failure gaps without putting a
// hidden override back in subscription rendering.
func (s *Service) NormalizeRealityFingerprintsForPanel(ctx context.Context, panelID int64, xrayVersion string) (int, error) {
	if !xraycompat.RequiresMLKEMFirst(xrayVersion) {
		return 0, nil
	}
	writer, ok := s.nodes.(ports.RealityFingerprintCASRepo)
	if !ok {
		return 0, fmt.Errorf("node repository does not support REALITY fingerprint convergence")
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return 0, err
	}
	changed := 0
	var errs []error
	for _, n := range nodes {
		if n == nil || n.PanelID != panelID || !inboundcfg.HasStoredConfig(n) {
			continue
		}
		normalized, didChange, err := xraycompat.NormalizeRealityFingerprint(n.StreamSettings, xrayVersion)
		if err != nil {
			errs = append(errs, fmt.Errorf("node %d: %w", n.ID, err))
			continue
		}
		if !didChange {
			continue
		}
		applied, err := writer.CompareAndSwapRealityStream(ctx, panelID, n.ID, n.StreamSettings, normalized)
		if err != nil {
			errs = append(errs, fmt.Errorf("node %d: %w", n.ID, err))
			continue
		}
		if !applied {
			// Another writer changed the snapshot after List. The next version
			// observation re-evaluates its current stream instead of restoring
			// fields from this stale copy.
			continue
		}
		changed++
		if err := s.pushNormalizedRealityInbound(ctx, n.ID); err != nil {
			errs = append(errs, fmt.Errorf("node %d: %w", n.ID, err))
		}
	}
	return changed, errors.Join(errs...)
}

func (s *Service) pushNormalizedRealityInbound(ctx context.Context, nodeID int64) error {
	n, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	spec, err := inboundcfg.SpecFromNode(n)
	if err != nil {
		return err
	}
	c, err := s.pool.Get(n.PanelID)
	if err == nil {
		err = c.UpdateInbound(ctx, n.InboundID, spec)
	}
	if err != nil {
		return s.enqueueNodeTask(ctx, domain.SyncTaskNodeUpdate, n, "normalize REALITY fingerprint", spec)
	}
	if async, ok := c.(ports.AsynchronousApplier); ok && async.ApplyIsAsynchronous() {
		return nil // Native applied receipt confirms the pending snapshot.
	}
	_, err = s.nodes.ConfirmAppliedConfig(ctx, n.ID, n.PanelID, n.ConfigIntent())
	return err
}

// markConfigPending flips the snapshot's sync-state column to "pending" so the
// UI surfaces "PSP wants this config but couldn't deliver it" while the retry
// task is in flight. Mirrors reconcile.markConfigSyncStatePending — kept inline
// here because the node service can't import reconcile (would cycle). The next
// successful push (via runNodeTask below, or a later reconcile cycle) flips it
// back to "synced" via ApplySpec / Capture / the runNodeTask success path.
// Best-effort: a DB failure here is logged-only, the same edit will re-trigger
// on the next admin save or the reconcile that comes after.
func (s *Service) markConfigPending(ctx context.Context, n *domain.Node) {
	if n.ConfigSyncState == domain.ConfigSyncPending {
		return
	}
	n.SetConfigSyncState(domain.ConfigSyncPending, time.Now())
	if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
		log.Warn("mark config pending failed", "node_id", n.ID, "err", err)
	}
}

func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if n.IsTunnelChild() {
		return fmt.Errorf("%w: node %q is the listener of a tunnel line: enable or disable the line", domain.ErrValidation, n.DisplayName)
	}
	// Read again under the locks of the server and of every tunnel line's entry
	// server, so a line added a moment ago is followed too.
	n, unlock, err := s.lockAndLoad(ctx, id, nil)
	if err != nil {
		return err
	}
	defer unlock()
	// The tunnel lines of a landing follow it: disabling the landing disables
	// their listeners and enabling it enables those of the lines that are on. A
	// listener behind a shared port asks first, and that is settled before
	// anything is changed.
	next := *n
	next.Enabled = enabled
	if err := s.tunnelEnabledRefusal(ctx, &next); err != nil {
		return err
	}
	if err := s.setEnabledLocked(ctx, n, enabled); err != nil {
		return err
	}
	n.Enabled = enabled
	return s.followTunnelLines(ctx, n)
}

// setEnabledLocked is the body of SetEnabled for a caller that holds the lock
// of the node's server.
func (s *Service) setEnabledLocked(ctx context.Context, n *domain.Node, enabled bool) error {
	// Disabling a listener that is behind a shared port sends the names routed
	// to it to the default site: it is allowed, and asks first.
	if !enabled && n.Enabled && s.frontSvc != nil && s.fronts != nil && !front.ConfirmationFrom(ctx).Confirmed {
		if refusal, err := s.frontSvc.DisableImpact(ctx, n); err != nil {
			return err
		} else if refusal != nil {
			return refusal
		}
	}
	// Enabling a member of a front that is not being sent, next to another
	// listener on its port, would leave both down: it asks first.
	if enabled && !n.Enabled && s.frontSvc != nil && s.fronts != nil && !front.ConfirmationFrom(ctx).Confirmed {
		if refusal, err := s.frontSvc.EnableRefusal(ctx, n); err != nil {
			return err
		} else if refusal != nil {
			return refusal
		}
	}
	c, clientErr := s.pool.Get(n.PanelID)
	if clientErr == nil && !ports.SupportsCapability(c, ports.CapabilityInboundEnable) {
		return fmt.Errorf("%w: selected panel cannot enable or disable individual inbounds", ports.ErrPanelCapabilityUnsupported)
	}
	// Column-scoped write: a full-row Save here would revert the health/traffic/
	// config columns the concurrent poll/health/reconcile loops are writing.
	wasEnabled := n.Enabled
	n.Enabled = enabled
	if err := s.nodes.UpdateEnabled(ctx, n.ID, enabled); err != nil {
		return err
	}
	if wasEnabled != enabled {
		// A disabled inbound is not one whose sniffing the panel is judged by.
		s.inboundsChanged(ctx, n.PanelID)
	}
	// A queued retry is a real outcome, so nil is right whenever the enqueue
	// lands. It is only wrong when the enqueue ITSELF fails — then the panel
	// still has the inbound enabled, nothing is pending, and the admin has been
	// told the node is off. See errUnqueuedPush.
	if clientErr != nil {
		if taskErr := s.enqueueNodeTask(ctx, domain.SyncTaskNodeSetEnabled, n, "sync node enabled state", map[string]bool{"enabled": enabled}); taskErr != nil {
			log.Warn("enqueue node enabled sync failed", "node_id", n.ID, "err", taskErr)
			return errUnqueuedPush("set node enabled state", clientErr, taskErr)
		}
		return nil
	}
	if err := c.SetInboundEnable(ctx, n.InboundID, enabled); err != nil {
		if taskErr := s.enqueueNodeTask(ctx, domain.SyncTaskNodeSetEnabled, n, "sync node enabled state", map[string]bool{"enabled": enabled}); taskErr != nil {
			log.Warn("enqueue node enabled sync failed", "node_id", n.ID, "err", taskErr)
			return errUnqueuedPush("set node enabled state", err, taskErr)
		}
		return nil
	}
	return nil
}

// ---- Delete flow ----

func (s *Service) DeleteAndSync(ctx context.Context, id int64) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if n.IsSeparator() {
		// Layout-only row: no 3X-UI inbound to delete, no clients to
		// reclaim, no sync task to enqueue. Drop the panel-side record.
		return s.deleteNode(ctx, id)
	}
	ctx, sink := withRouteSink(ctx)
	defer s.cleanup(ctx, sink)
	if n.IsTunnelChild() {
		return s.removeOrphanListener(ctx, n)
	}
	n, unlock, err := s.lockAndLoad(ctx, id, nil)
	if err != nil {
		return err
	}
	defer unlock()
	// Deleting a landing deletes the listeners of its tunnel lines: it asks
	// first, naming them.
	if err := s.tunnelDeleteRefusal(ctx, n, "delete_node"); err != nil {
		return err
	}
	change, err := s.checkRemovalAgainstFronts(ctx, n)
	if err != nil {
		return err
	}
	if err := s.cleaner.EnsureInboundDeletable(ctx, n.PanelID, n.InboundID); err != nil {
		if errors.Is(err, domain.ErrInboundHasUnmanagedClients) {
			return err
		}
		// Remote reachability problems are left to the queued task. The task
		// will run the same guard before deleting any managed clients.
		log.Warn("node delete preflight failed; queueing guarded delete", "node_id", n.ID, "err", err)
	}
	if err := s.finishFront(ctx, n, change); err != nil {
		return err
	}
	if err := s.removeTunnelListeners(ctx, n); err != nil {
		return err
	}
	n.Enabled = false
	// Column-scoped write (see SetEnabled): don't clobber concurrent
	// health/traffic/config column writes with a stale full-row Save.
	if err := s.nodes.UpdateEnabled(ctx, n.ID, false); err != nil {
		return err
	}
	s.inboundsChanged(ctx, n.PanelID)
	return s.enqueueNodeTask(ctx, domain.SyncTaskNodeDelete, n, "delete node", nil)
}

// DetachAndSync drops the node record and the panel's ownership rows without
// requiring a reachable upstream. Clients and any remaining rules must be
// cleaned up manually in 3X-UI. This is intended for
// decommissioned/offline servers where queued remote deletes would retry
// forever. Separators have no upstream binding and use a plain local delete.
func (s *Service) DetachAndSync(ctx context.Context, id int64) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if n.IsSeparator() {
		return s.deleteNode(ctx, id)
	}
	// The routing lock first (before any server lock), held until the cleanup of
	// the tunnels only the node's routes used is done. That cleanup takes the same
	// server locks the detach holds, which are not reentrant: it runs after the
	// detach has let go of them.
	ctx, sink := withRouteSink(ctx)
	defer s.cleanup(ctx, sink)
	if n.IsTunnelChild() {
		return s.removeOrphanListener(ctx, n)
	}
	defer s.lockRoutes(n.PanelID)()
	routed, err := s.detachLocked(ctx, n)
	if err != nil {
		return err
	}
	s.afterRemove(ctx, routed)
	return nil
}

// detachLocked is the part of DetachAndSync that holds the node's server lock.
// It returns the deleted node when it had routes, for afterRemove.
func (s *Service) detachLocked(ctx context.Context, peek *domain.Node) (*domain.Node, error) {
	n, unlock, err := s.lockAndLoad(ctx, peek.ID, nil)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.tunnelDeleteRefusal(ctx, n, "delete_node"); err != nil {
		return nil, err
	}
	return s.detachNoLock(ctx, n)
}

// detachNoLock detaches a node whose server's lock (and that of the entry of
// each tunnel line it owns) the caller holds. The listeners of its tunnel lines
// go with it.
func (s *Service) detachNoLock(ctx context.Context, n *domain.Node) (*domain.Node, error) {
	change, err := s.checkRemovalAgainstFronts(ctx, n)
	if err != nil {
		return nil, err
	}
	if err := s.cleaner.UnclaimAllForInbound(ctx, n.PanelID, n.InboundID); err != nil {
		return nil, fmt.Errorf("unclaim owned clients: %w", err)
	}
	if err := s.finishFront(ctx, n, change); err != nil {
		return nil, err
	}
	if err := s.removeTunnelListeners(ctx, n); err != nil {
		return nil, err
	}
	routed, err := s.removeNode(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	s.inboundsChanged(ctx, n.PanelID)
	return routed, nil
}

// ProcessDueTasks runs pending node-scoped 3X-UI write tasks.
func (s *Service) ProcessDueTasks(ctx context.Context, limit int) error {
	if s.tasks == nil {
		return nil
	}
	tasks, err := s.tasks.ListDue(ctx, time.Now(), limit)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Type != domain.SyncTaskNodeCreate &&
			task.Type != domain.SyncTaskNodeDelete &&
			task.Type != domain.SyncTaskNodeSetEnabled &&
			task.Type != domain.SyncTaskNodeUpdate {
			continue
		}
		claimed, err := s.tasks.MarkRunning(ctx, task.ID)
		if err != nil {
			// Per-task bookkeeping error: log + continue so one transient DB
			// blip doesn't stall the rest of the due batch.
			log.Warn("node task mark-running", "task_id", task.ID, "err", err)
			continue
		}
		if !claimed {
			// Canceled by admin (or claimed by another runner) between ListDue
			// and here — skip so the 3X-UI mutation the admin canceled never runs.
			continue
		}
		if err := s.runNodeTask(ctx, task); err != nil {
			if isPermanentNodeTaskError(err) {
				if markErr := s.tasks.Cancel(ctx, task.ID); markErr != nil {
					log.Warn("node task cancel", "task_id", task.ID, "err", markErr)
				}
				s.markConfigSyncGaveUp(ctx, task, err)
				continue
			}
			// Cap retries the same way the user processor does (maxUserTaskAttempts):
			// a transiently-classified but permanently-broken node task otherwise
			// retries every minute forever, burning CPU + 3X-UI quota. Cancel after
			// the cap with the last error preserved for the Sync Tasks view; admin's
			// explicit "Retry" still overrides.
			if task.Attempts+1 >= maxNodeTaskAttempts {
				log.Warn("node task gave up after max attempts",
					"task_id", task.ID, "type", task.Type,
					"target_id", task.TargetID, "attempts", task.Attempts+1,
					"last_err", err.Error())
				if markErr := s.tasks.Cancel(ctx, task.ID); markErr != nil {
					log.Warn("node task cancel (max attempts)", "task_id", task.ID, "err", markErr)
				}
				s.markConfigSyncGaveUp(ctx, task, err)
				continue
			}
			next := time.Now().Add(nodeTaskBackoff(task.Attempts + 1))
			if markErr := s.tasks.MarkRetry(ctx, task.ID, err.Error(), next); markErr != nil {
				log.Warn("node task mark-retry", "task_id", task.ID, "err", markErr)
			}
			continue
		}
		if err := s.tasks.MarkSucceeded(ctx, task.ID); err != nil {
			log.Warn("node task mark-succeeded", "task_id", task.ID, "err", err)
		}
	}
	return nil
}

func (s *Service) runNodeTask(ctx context.Context, task *domain.SyncTask) error {
	if task.Status == domain.SyncTaskRetired {
		return nil
	}
	if task.ID > 0 && s.tasks != nil {
		// ListDue may have returned this pointer before an offline conversion
		// retired its persisted row. Recheck before either create or delete;
		// even a purged tombstone must not resurrect the cached payload.
		current, err := s.tasks.GetByID(ctx, task.ID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if current == nil || current.Status == domain.SyncTaskRetired {
			return nil
		}
	}
	if task.Type == domain.SyncTaskNodeCreate {
		return s.runNodeCreateTask(ctx, task)
	}
	n, err := s.nodes.GetByID(ctx, task.TargetID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return err
	}
	switch task.Type {
	case domain.SyncTaskNodeDelete:
		// A native panel applies asynchronously: PSP owns the inbound, the agent
		// deploys PSP's desired config, and DelInbound is a no-op. Deleting the
		// node row IS the upstream delete (the agent drops it on its next sync), so
		// nothing here may depend on the agent's report. Its GetInbound answers
		// from that report, and its errors ("not found", "no cached full report:
		// not found", offline, stale) never match isInboundGoneError, so gating on
		// it would retry a never-reported node to the attempt cap and leave the row
		// disabled.
		if async, ok := c.(ports.AsynchronousApplier); ok && async.ApplyIsAsynchronous() {
			if err := s.cleaner.UnclaimAllForInbound(ctx, n.PanelID, n.InboundID); err != nil {
				return fmt.Errorf("unclaim owned clients: %w", err)
			}
			return s.deleteNode(ctx, n.ID)
		}
		// If the inbound is already gone upstream (operator deleted it directly
		// in 3X-UI — the common orphan case), the clients went with it: skip the
		// guard and per-client deletes (which would each fail "record not found"
		// and the task would retry forever, never reaching nodes.Delete). Just
		// drop the local ownership rows and the PSP node row; the desired end
		// state is already true upstream.
		if _, err := c.GetInbound(ctx, n.InboundID); err != nil {
			if isInboundGoneError(err) {
				if err := s.cleaner.UnclaimAllForInbound(ctx, n.PanelID, n.InboundID); err != nil {
					return fmt.Errorf("unclaim owned clients: %w", err)
				}
				return s.deleteNode(ctx, n.ID)
			}
			return err
		}
		if err := s.cleaner.EnsureInboundDeletable(ctx, n.PanelID, n.InboundID); err != nil {
			return err
		}
		if err := s.cleaner.DelAllOwnedForInbound(ctx, n.PanelID, n.InboundID); err != nil {
			return fmt.Errorf("clear owned clients: %w", err)
		}
		if err := s.cleaner.DeleteInbound(ctx, n.PanelID, n.InboundID); err != nil {
			// Lost the race: inbound disappeared between our GetInbound and the
			// delete. Treat as done rather than retrying forever.
			if isInboundGoneError(err) {
				return s.deleteNode(ctx, n.ID)
			}
			return err
		}
		return s.deleteNode(ctx, n.ID)
	case domain.SyncTaskNodeSetEnabled:
		if err := c.SetInboundEnable(ctx, n.InboundID, n.Enabled); err != nil {
			return fmt.Errorf("xui setEnable: %w", err)
		}
		return nil
	case domain.SyncTaskNodeUpdate:
		// Use the local snapshot, NOT task.Payload. The snapshot is the v3.5
		// source of truth and reflects the latest admin edit even if multiple
		// edits stacked between enqueue and run (or if dedup collapsed them
		// onto this one task). Pushing the captured-at-enqueue payload could
		// regress 3X-UI to a superseded spec. Capture the version stamp we're
		// about to push so the post-push state flip can detect a concurrent edit.
		stamp := n.ConfigSyncedAt
		spec, serr := inboundcfg.SpecFromNode(n)
		if serr != nil {
			// A task that can never produce a valid push must not spin: this is
			// terminal for the task, not a transient panel failure.
			return fmt.Errorf("%w: %w", domain.ErrValidation, serr)
		}
		if err := c.UpdateInbound(ctx, n.InboundID, spec); err != nil {
			return err
		}
		if async, ok := c.(ports.AsynchronousApplier); ok && async.ApplyIsAsynchronous() {
			return nil
		}
		// The push is a multi-second round-trip that may straddle an admin
		// UpdateInboundConfig (S2). Re-read and only flip config_sync_state when
		// the snapshot we pushed is still current: on a stamp mismatch a newer
		// edit owns the row (its own task / reconcile converges it), and writing
		// our stale snapshot back would revert S2 in the DB. Writing the freshly
		// re-read node (not the load-time one) also keeps the success write from
		// reverting the health pass's port/protocol to a stale value.
		fresh, err := s.nodes.GetByID(ctx, n.ID)
		if err != nil || fresh == nil || !sameSyncStamp(stamp, fresh.ConfigSyncedAt) {
			return nil
		}
		if fresh.ConfigSyncState != domain.ConfigSyncSynced {
			fresh.SetConfigSyncState(domain.ConfigSyncSynced, time.Now())
			_ = s.nodes.UpdateInboundConfig(ctx, fresh)
		}
		return nil
	default:
		return nil
	}
}

type nodeCreateTaskPayload struct {
	Node domain.Node       `json:"node"`
	Spec ports.InboundSpec `json:"spec"`
}

func decodeNodeCreateTask(payload string) (nodeCreateTaskPayload, error) {
	var p nodeCreateTaskPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return p, err
	}
	// The persisted task uses the Node JSON shape. Keep key presence separate
	// from decoded values so an explicit clear is not treated as an omission.
	var wire struct {
		Node struct {
			Relays []struct {
				Region json.RawMessage
				Tags   json.RawMessage
			}
		} `json:"node"`
	}
	if err := json.Unmarshal([]byte(payload), &wire); err != nil {
		return p, err
	}
	for i, relay := range wire.Node.Relays {
		p.Node.Relays[i].RegionUnresolved = relay.Region == nil
		p.Node.Relays[i].TagsUnresolved = relay.Tags == nil
	}
	domain.ResolveRelayLabels(nil, p.Node.Relays, p.Node.Region, p.Node.Tags)
	return p, nil
}

func (s *Service) runNodeCreateTask(ctx context.Context, task *domain.SyncTask) error {
	p, err := decodeNodeCreateTask(task.Payload)
	if err != nil {
		return err
	}
	n := p.Node
	n.ID = 0
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return err
	}
	inboundID, err := c.AddInbound(ctx, p.Spec)
	if err != nil {
		// Orphan recovery: if AddInbound returned "port already exists",
		// the first attempt may have succeeded on 3X-UI but its response was
		// lost (network blip, panel restart, timeout). Without recovery the
		// task is cancelled as permanent and the operator is left with an
		// inbound on 3X-UI that no node row points at. Check for an inbound
		// matching our spec on the same panel; if one exists and isn't owned
		// by some other PSP node, adopt it instead of giving up.
		if isPortAlreadyExistsError(err) {
			if adopted, lookupErr := s.tryAdoptOrphan(ctx, c, n.PanelID, p.Spec); lookupErr == nil && adopted != nil {
				n.InboundID = adopted.ID
				n.Enabled = true
				inboundcfg.Adopt(&n, adopted)
				if err := s.nodes.Create(ctx, &n); err != nil {
					return err
				}
				s.syncExistingUsersToNodeInBackground(&n)
				log.Info("create node: adopted existing inbound (recovery from lost AddInbound response)",
					"panel_id", n.PanelID, "inbound_id", adopted.ID, "port", adopted.Port)
				return nil
			}
			// Genuine port conflict (different protocol on same port, or
			// another PSP node already owns the matching inbound). Bail out
			// permanently so the operator sees the failure.
			return permanentInboundCreateError(err)
		}
		if permanentErr := permanentInboundCreateError(err); permanentErr != nil {
			return permanentErr
		}
		return fmt.Errorf("xui addInbound: %w", err)
	}
	n.InboundID = inboundID
	n.Enabled = true
	inboundcfg.ApplySpec(&n, p.Spec)
	if err := s.nodes.Create(ctx, &n); err != nil {
		_ = c.DelInbound(context.Background(), inboundID)
		return err
	}
	s.syncExistingUsersToNodeInBackground(&n)
	return nil
}

// tryAdoptOrphan searches the panel's live inbound list for one matching
// `spec` (port + protocol + listen) that no other PSP node already owns,
// and returns it for adoption. Returns (nil, nil) if no match — caller
// should treat that as a genuine port conflict. Strict matching (not just
// port) keeps the false-adoption risk low when an operator happens to
// have a different inbound on the same port.
//
// Uses ListInboundsSlim, not ListInbounds: matching only reads
// port/protocol/listen and Capture strips settings.clients[] anyway, so
// pulling every inbound's full client list (uuid/flow/password) would be
// wasted transfer on a large panel — slim returns the identical shape minus
// clients[].
func (s *Service) tryAdoptOrphan(ctx context.Context, c ports.XUIClient, panelID int64, spec ports.InboundSpec) (*ports.Inbound, error) {
	inbs, err := c.ListInboundsSlim(ctx)
	if err != nil {
		return nil, err
	}
	for i := range inbs {
		inb := &inbs[i]
		if inb.Port != spec.Port {
			continue
		}
		if !strings.EqualFold(inb.Protocol, spec.Protocol) {
			continue
		}
		if inb.Listen != spec.Listen {
			continue
		}
		existing, err := s.nodes.GetByPanelInbound(ctx, panelID, inb.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			// A real DB error (not "unclaimed") — surface it rather than
			// treating the inbound as free and risking a double-adopt that the
			// uk_panel_inbound index would then reject as a confusing failure.
			return nil, err
		}
		if existing != nil {
			// Another PSP node already claimed this inbound — likely a
			// concurrent create won. Don't double-adopt.
			continue
		}
		return inb, nil
	}
	return nil, nil
}

// isPortAlreadyExistsError reports whether err is 3X-UI's "this port is already
// taken by another inbound" rejection from AddInbound. 3X-UI's wording has
// varied across versions: older panels said "port already exists", while
// >= 3.4.2 return the transport-aware form
// "port <p> (<proto>) already used by inbound '<name>' (#<id>) on <listen>"
// (web/service/port_conflict.go — byte-identical through 3.5.0). Match BOTH so
// port-conflict classification (fail-fast + orphan adoption) survives the
// wording change; the "already used by inbound" form is LIVE-VERIFIED on a real
// 3.5.0 panel. Kept as the single source of truth — permanentInboundCreateError
// reuses it — so a future wording change is a one-line fix here.
func isPortAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "port already exists") ||
		strings.Contains(msg, "already used by inbound")
}

func (s *Service) enqueueNodeCreateTask(ctx context.Context, n *domain.Node, spec ports.InboundSpec, cause error) error {
	payload, err := json.Marshal(nodeCreateTaskPayload{Node: *n, Spec: spec})
	if err != nil {
		return err
	}
	if s.tasks == nil {
		return cause
	}
	return s.tasks.Create(ctx, &domain.SyncTask{
		Type:       domain.SyncTaskNodeCreate,
		Status:     domain.SyncTaskPending,
		TargetType: "node",
		TargetID:   0,
		Summary:    fmt.Sprintf("create node %s", n.DisplayName),
		Payload:    string(payload),
		LastError:  cause.Error(),
		NextRunAt:  time.Now(),
	})
}

func permanentInboundCreateError(err error) error {
	if err == nil {
		return nil
	}
	// A port conflict is permanent — the port won't free itself on retry — so
	// surface it as ErrAlreadyExists (fail-fast) rather than a transient error
	// the task runner would retry to the attempt cap. isPortAlreadyExistsError
	// owns BOTH the old "port already exists" and the >= 3.4.2 "already used by
	// inbound" wordings, so classification stays in one place.
	if isPortAlreadyExistsError(err) {
		return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, strings.TrimSpace(err.Error()))
	}
	return nil
}

func isPermanentNodeTaskError(err error) bool {
	return errors.Is(err, domain.ErrAlreadyExists) ||
		errors.Is(err, domain.ErrValidation) ||
		errors.Is(err, domain.ErrInboundHasUnmanagedClients)
}

func (s *Service) enqueueNodeTask(ctx context.Context, typ domain.SyncTaskType, n *domain.Node, summary string, payload any) error {
	if s.tasks == nil {
		return nil
	}
	// Dedup uniformly across task types. NodeUpdate used to bypass dedup so
	// rapid edits could each enqueue their own spec — now the runner reads
	// the latest snapshot at execution time (see SyncTaskNodeUpdate case
	// above), so collapsing to a single pending task is the correct
	// behaviour: whoever's snapshot is latest wins.
	if _, err := s.tasks.GetActiveByTarget(ctx, typ, "node", n.ID); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	var payloadJSON string
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		payloadJSON = string(b)
	}
	return s.tasks.Create(ctx, &domain.SyncTask{
		Type:       typ,
		Status:     domain.SyncTaskPending,
		TargetType: "node",
		TargetID:   n.ID,
		Summary:    fmt.Sprintf("%s %s", summary, n.DisplayName),
		Payload:    payloadJSON,
		NextRunAt:  time.Now(),
	})
}

// EnqueueConfigPush schedules an async push of a node's current local inbound
// config to 3X-UI via the retryable SyncTaskNodeUpdate path (which re-reads the
// live snapshot and pushes it, retrying transient failures). The cert service
// calls this after writing a managed certificate into the node's StreamSettings
// so the inline cert is delivered through the same machinery — a transient push
// failure retries the push, NEVER re-issuing the certificate (which would burn
// the ACME rate limit). nil payload: SyncTaskNodeUpdate ignores it and pushes
// the latest snapshot.
func (s *Service) EnqueueConfigPush(ctx context.Context, nodeID int64) error {
	n, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.enqueueNodeTask(ctx, domain.SyncTaskNodeUpdate, n, "deploy managed certificate", nil)
}

// nodeTaskBackoff returns a flat 1-minute retry interval — same rationale
// as deleteTaskBackoff in user.go.
func nodeTaskBackoff(_ int) time.Duration {
	return time.Minute
}

// maxNodeTaskAttempts caps node-scoped sync-task retries, mirroring
// maxUserTaskAttempts. At the flat 1-minute backoff this is ~1.5h of recovery —
// past any plausible transient 3X-UI outage but bounded so a permanently-broken
// task (e.g. a panel that keeps 401ing, an inbound config 3X-UI rejects)
// doesn't loop forever burning CPU + 3X-UI quota. Admin "Retry" overrides.
const maxNodeTaskAttempts = 100

// isInboundGoneError reports whether err from a GetInbound/DeleteInbound call
// means the inbound no longer exists upstream — the common case where an
// operator deleted the inbound directly in 3X-UI. 3X-UI answers a missing
// inbound with HTTP 200 {success:false, msg:"...record not found..."} (or a
// 404 for an unknown id), neither of which isPermanentNodeTaskError classifies,
// so without this the node-delete task retries to the attempt cap and the ghost
// PSP node row is never dropped.
func isInboundGoneError(err error) bool {
	if err == nil {
		return false
	}
	// Match 3X-UI's specific absent-inbound/client phrasings (GORM's "record not
	// found" sentinel, "not found in inbound") or an explicit HTTP 404 — NOT a
	// bare "not found", which a reverse-proxy or localized "404 Not Found" HTML
	// error body would also satisfy. A false positive here is destructive: the
	// delete branch would UnclaimAllForInbound + drop the node row while the live
	// inbound (and its PSP-created clients) still exist upstream.
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "record not found") ||
		strings.Contains(m, "not found in inbound") ||
		strings.Contains(m, "http 404")
}

// sameSyncStamp reports whether two ConfigSyncedAt stamps are equal (both nil,
// or equal times). Used by the SyncTaskNodeUpdate success path to detect an
// admin edit that landed during the push before flipping config_sync_state.
func sameSyncStamp(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// ---- New-node user sync ----

// syncExistingUsersToNodeInBackground runs syncExistingUsersToNode off the
// request thread. CreateInbound / ImportExisting (and their task-replay
// equivalents) call this so the admin save returns at once — pushing N user
// clients sequentially to 3X-UI would otherwise block the response on N
// HTTP round-trips, which gets painful for groups with many members.
//
// Uses a fresh context (the request context is cancelled when the response
// is written) and a value-copy of n (the caller may mutate / drop the
// pointer before the goroutine runs). Anything the goroutine can't finish —
// process restart, panel down, transient failure — is healed by
// HealSharedClients, which re-runs every user's membership on the reconcile
// ticker (every fourth cycle once the shared-client migration is complete).
// Reconcile axis B's checkMissingOwnerships does NOT heal it: it only covers
// users who still have legacy ownership rows. Mirrors
// user.ResyncGroupMembersInBackground (beta.7).
func (s *Service) syncExistingUsersToNodeInBackground(n *domain.Node) {
	snap := *n
	s.runBackground("node.sync-existing-users", func(ctx context.Context) {
		if err := s.syncExistingUsersToNode(ctx, &snap); err != nil {
			log.Warn("sync existing users (background)", "node_id", snap.ID, "err", err)
		}
	})
}

// syncExistingUsersToNode walks every group; for groups whose tag_filter would
// include this node, every enabled member gets a user_resync task. Errors per
// user are logged and the loop continues; HealSharedClients heals anything left
// behind (see syncExistingUsersToNodeInBackground).
func (s *Service) syncExistingUsersToNode(ctx context.Context, n *domain.Node) error {
	protocol, err := s.newNodeProtocol(ctx, n)
	if err != nil {
		return err
	}
	if protocol == "" {
		log.Warn("new-node sync skip: unsupported protocol", "node_id", n.ID)
		return nil
	}

	groups, err := s.groups.List(ctx)
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}

	// v3.9.0 INVERTED ENROLLMENT: do NOT create per-node clients here — that path
	// is retired and would write the dropped user_xui_clients table (failing on a
	// migrated install) and regrow it. Instead enqueue a resync per eligible
	// member; the sync-task loop runs ResyncMembership, which re-provisions each
	// member's SHARED client to include this new node's inbound (idempotent).
	seen := make(map[int64]bool)
	considered, enqueued := 0, 0
	for _, g := range groups {
		if !s.groupIncludes(ctx, n, g) {
			continue
		}
		members, err := s.users.ListByGroup(ctx, g.ID)
		if err != nil {
			log.Warn("new-node sync list members", "group_id", g.ID, "err", err)
			continue
		}
		for _, u := range members {
			considered++
			if !domain.AccountLoginAllowed(u.Enabled, u.AutoDisabledReason) || seen[u.ID] {
				continue
			}
			seen[u.ID] = true
			if err := s.enqueueUserResync(ctx, u.ID, "attach shared client to new node "+n.DisplayName); err != nil {
				log.Warn("new-node enqueue resync", "user_id", u.ID, "err", err)
				continue
			}
			enqueued++
		}
	}
	log.Info("synced existing users on node", "node_id", n.ID,
		"considered_members", considered, "enqueued_resyncs", enqueued)
	return nil
}

// newNodeProtocol is the protocol syncExistingUsersToNode checks before it
// enrols the members.
//
// A NATIVE panel answers from its own desired state. Its GetInbound reads the
// agent's last full report, which has no entry for a node created a moment ago
// (and none at all before the agent's first report), so asking it refused the
// enqueue, and nothing retried: existing members got no client on the new node
// until a heal or a recreate. The resync writes the members' shared clients and
// attachments before it reads anything back, so the roster reaches the agent
// whether or not the agent has reported yet; a later read-back that the report
// cannot answer yet is retried by the task loop. The node row already says what
// PSP will deploy.
//
// 3X-UI and S-UI keep reading the live inbound: there the inbound exists before
// the node row does, and it is the source of the protocol (and of the
// Shadowsocks method).
func (s *Service) newNodeProtocol(ctx context.Context, n *domain.Node) (domain.Protocol, error) {
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return "", fmt.Errorf("inspect inbound: %w", err)
	}
	if async, ok := c.(ports.AsynchronousApplier); ok && async.ApplyIsAsynchronous() {
		return crypto.DetectProtocol(n.DesiredProtocol, ""), nil
	}
	info, err := s.inspectInbound(ctx, n)
	if err != nil {
		return "", fmt.Errorf("inspect inbound: %w", err)
	}
	return info.protocol, nil
}

// enqueueUserResync queues a deduped user_resync task (mirrors user.enqueueUserTask).
// The sync-task loop runs ResyncMembership, which provisions the user's shared
// client across their current node set — the v3.9.0 replacement for the per-node
// new-node enrollment that wrote the retired ownership table.
func (s *Service) enqueueUserResync(ctx context.Context, userID int64, summary string) error {
	if s.tasks == nil {
		return nil
	}
	if _, err := s.tasks.GetActiveByTarget(ctx, domain.SyncTaskUserResync, "user", userID); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.tasks.Create(ctx, &domain.SyncTask{
		Type:       domain.SyncTaskUserResync,
		Status:     domain.SyncTaskPending,
		TargetType: "user",
		TargetID:   userID,
		Summary:    summary,
		NextRunAt:  time.Now(),
	})
}

type inboundInfo struct {
	protocol domain.Protocol
	flow     string
	ssMethod string
}

// inspectInbound reads the inbound from 3X-UI and extracts the bits needed
// to compose a ClientSpec: protocol (with SS / SS-2022 distinguished by the
// cipher method) and the default xtls flow (inferred for VLESS+Reality
// when settings.clients[] is empty, which is the new-inbound case).
func (s *Service) inspectInbound(ctx context.Context, n *domain.Node) (*inboundInfo, error) {
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, err
	}
	inb, err := c.GetInbound(ctx, n.InboundID)
	if err != nil {
		return nil, err
	}
	info := &inboundInfo{}
	if inb.Settings != "" {
		var s struct {
			Method  string `json:"method"`
			Clients []struct {
				Flow string `json:"flow"`
			} `json:"clients"`
		}
		_ = json.Unmarshal([]byte(inb.Settings), &s)
		info.protocol = crypto.DetectProtocol(inb.Protocol, s.Method)
		info.ssMethod = s.Method
		for _, c := range s.Clients {
			if c.Flow != "" {
				info.flow = c.Flow
				break
			}
		}
	} else {
		info.protocol = crypto.DetectProtocol(inb.Protocol, "")
	}
	if info.flow == "" {
		info.flow = n.Flow
	}
	// Reality default — when the inbound has no clients yet, settings.clients[]
	// can't tell us the flow. VLESS+Reality effectively always wants
	// xtls-rprx-vision.
	if info.flow == "" && info.protocol == domain.ProtoVLESS && inb.StreamSettings != "" {
		var ss struct {
			Security string `json:"security"`
		}
		if json.Unmarshal([]byte(inb.StreamSettings), &ss) == nil && ss.Security == "reality" {
			info.flow = "xtls-rprx-vision"
		}
	}
	return info, nil
}

// ---- Discovery (unchanged from before) ----

type UnmanagedInbound struct {
	PanelID     int64
	PanelName   string
	InboundID   int
	Protocol    string
	Port        int
	Remark      string
	Enable      bool
	ClientCount int
}

// ListUnmanagedInbounds returns the inbounds on a single panel that aren't yet
// managed by a node. Scoping to one panel (rather than scanning the whole
// fleet) keeps the call bounded: exactly one 3X-UI round trip, and a slow or
// unreachable panel only affects the server the admin actually selected
// instead of failing the entire list.
func (s *Service) ListUnmanagedInbounds(ctx context.Context, panelID int64) ([]*UnmanagedInbound, error) {
	c, err := s.pool.Get(panelID)
	if err != nil {
		return nil, fmt.Errorf("xui panel %d: %w", panelID, err)
	}
	panelName := ""
	for _, p := range s.pool.List() {
		if p.ID == panelID {
			panelName = p.Name
			break
		}
	}
	// Build the set of already-managed inbound IDs for this panel in one DB
	// read, then test membership in O(1) — avoids a GetByPanelInbound round
	// trip per inbound (the old per-inbound query was the N×M cost).
	managed, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	claimed := make(map[int]struct{})
	for _, n := range managed {
		if n.PanelID == panelID && !n.IsSeparator() {
			claimed[n.InboundID] = struct{}{}
		}
	}
	// Slim: this listing reads only inbound-level fields (id/protocol/port/
	// remark/enable) + len(ClientStats); it never touches settings.clients[], so
	// the slim payload (clients trimmed, clientStats kept) is a strict win on
	// panels with many clients.
	inbounds, err := c.ListInboundsSlim(ctx)
	if err != nil {
		return nil, fmt.Errorf("list inbounds for panel %d: %w", panelID, err)
	}
	out := []*UnmanagedInbound{}
	for i := range inbounds {
		inb := &inbounds[i]
		if _, ok := claimed[inb.ID]; ok {
			continue
		}
		// Skip protocols the panel can't actually manage (wireguard, socks,
		// dokodemo-door, http, etc.). The admin UI offers Claim / Import
		// against this list, and both flows require a Protocol the
		// subscription renderer + 3X-UI client adapter understand. Listing
		// unsupported inbounds here would just produce errors at import time.
		if crypto.DetectProtocol(inb.Protocol, "") == "" {
			continue
		}
		out = append(out, &UnmanagedInbound{
			PanelID:     panelID,
			PanelName:   panelName,
			InboundID:   inb.ID,
			Protocol:    inb.Protocol,
			Port:        inb.Port,
			Remark:      inb.Remark,
			Enable:      inb.Enable,
			ClientCount: len(inb.ClientStats),
		})
	}
	return out, nil
}

type InboundClientView struct {
	Email       string
	Up          int64
	Down        int64
	Enable      bool
	ExpiryTime  int64
	Managed     bool
	OwnerUserID int64
}

func (s *Service) ListClientsOfInbound(ctx context.Context, nodeID int64, ownership ports.OwnershipRepo, pspClients ports.PSPClientRepo) ([]*InboundClientView, error) {
	n, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, err
	}
	inb, err := c.GetInbound(ctx, n.InboundID)
	if err != nil {
		return nil, err
	}
	// Load every ownership row for this inbound in ONE query and index by email,
	// instead of a per-client GetByMatch (N+1: one SELECT per client in the
	// inbound). ListByInbound is the same set GetByMatch would resolve one at a
	// time. Tolerate a load error by falling back to "no rows" (all unmanaged)
	// so the listing still renders.
	ownedByEmail := make(map[string]*domain.XUIClientEntry)
	if entries, err := ownership.ListByInbound(ctx, n.PanelID, n.InboundID); err == nil {
		for _, e := range entries {
			ownedByEmail[e.ClientEmail] = e
		}
	}
	out := make([]*InboundClientView, 0, len(inb.ClientStats))
	for _, cs := range inb.ClientStats {
		view := &InboundClientView{
			Email:      cs.Email,
			Up:         cs.Up,
			Down:       cs.Down,
			Enable:     cs.Enable,
			ExpiryTime: cs.ExpiryTime,
		}
		if entry, ok := ownedByEmail[cs.Email]; ok {
			view.Managed = true
			view.OwnerUserID = entry.UserID
		} else if pspClients != nil {
			// MIGRATION(v3→v4): ownership-OR-psp fallback. When the legacy path goes,
			// resolve managed/owner from psp_client only (drop the ownedByEmail branch).
			// v3.9.0: a migrated user's client has no ownership row — it's a shared
			// client. Resolve managed/owner from psp_client so the node-detail list
			// doesn't mislabel every PSP client as an operator's unmanaged client.
			if pc, perr := pspClients.GetByEmail(ctx, n.PanelID, cs.Email); perr == nil && pc != nil {
				view.Managed = true
				view.OwnerUserID = pc.UserID
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// validateManagedRelays checks a landing node's managed relay lines against
// the rest of the fleet: the entry must be a Passwall Node server, and the
// entry port must be free on it, both of the server's own inbounds and of
// other managed relay lines. Disabled inbounds and lines count too: enabling
// one later must not silently collide.
// validateEgress checks a node's exit server: traffic of a node on one
// Passwall Node server may leave from another, through a tunnel PSP runs
// between the two. 0 leaves from the node's own server.
func (s *Service) validateEgress(node *domain.Node, egress int64) error {
	if egress == 0 {
		return nil
	}
	if egress < 0 {
		return fmt.Errorf("%w: invalid exit server", domain.ErrValidation)
	}
	if node.IsSeparator() {
		return fmt.Errorf("%w: a separator has no exit server", domain.ErrValidation)
	}
	if egress == node.PanelID {
		return fmt.Errorf("%w: the exit server must differ from the node's own server", domain.ErrValidation)
	}
	kinds := make(map[int64]domain.PanelKind)
	if s.pool != nil {
		for _, panel := range s.pool.List() {
			if panel != nil {
				kinds[panel.ID] = domain.NormalizePanelKind(panel.Kind)
			}
		}
	}
	if kinds[node.PanelID] != domain.PanelKindPSP {
		return fmt.Errorf("%w: only a node on a Passwall Node server can leave from another server", domain.ErrValidation)
	}
	if kinds[egress] != domain.PanelKindPSP {
		return fmt.Errorf("%w: the exit server must be a Passwall Node server", domain.ErrValidation)
	}
	return nil
}

func (s *Service) validateManagedRelays(ctx context.Context, landing *domain.Node, dropping ...int64) error {
	if err := s.checkRelaysAgainstFronts(ctx, landing); err != nil {
		return err
	}
	managed := false
	for _, line := range landing.Relays {
		managed = managed || line.Managed()
	}
	if !managed {
		return nil
	}
	if err := domain.ValidateManagedRelays(landing, landing.Relays); err != nil {
		return err
	}
	kinds := make(map[int64]domain.PanelKind)
	if s.pool != nil {
		for _, panel := range s.pool.List() {
			if panel != nil {
				kinds[panel.ID] = domain.NormalizePanelKind(panel.Kind)
			}
		}
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return err
	}
	type bind = bindKey
	// A shared port (SNI front) holds the listeners behind it and any number of
	// relay lines, told apart by name, so neither collides there; the loopback
	// ports of fronted listeners are plain taken ports.
	shared, loopback, err := s.frontedBinds(ctx)
	if err != nil {
		return err
	}
	taken := make(map[bind]string)
	for key, owner := range loopback {
		taken[key] = owner
	}
	for _, other := range nodes {
		if other == nil || other.IsSeparator() || (landing.ID != 0 && other.ID == landing.ID) || slices.Contains(dropping, other.ID) {
			continue
		}
		if other.DesiredPort > 0 && !shared[bind{other.PanelID, other.DesiredPort}] {
			taken[bind{other.PanelID, other.DesiredPort}] = fmt.Sprintf("inbound of node %q", other.DisplayName)
		}
		for _, line := range other.Relays {
			if line.Managed() && !shared[bind{line.EntryPanelID, line.Port}] {
				taken[bind{line.EntryPanelID, line.Port}] = fmt.Sprintf("relay line of node %q", other.DisplayName)
			}
		}
	}
	for i, line := range landing.Relays {
		if !line.Managed() {
			continue
		}
		if kinds[line.EntryPanelID] != domain.PanelKindPSP {
			return fmt.Errorf("%w: relay line %d: the entry server must be a Passwall Node server", domain.ErrValidation, i+1)
		}
		key := bind{line.EntryPanelID, line.Port}
		if owner, exists := taken[key]; exists {
			return fmt.Errorf("%w: relay line %d: port %d on the entry server is already used by the %s", domain.ErrValidation, i+1, line.Port, owner)
		}
		if shared[key] {
			continue
		}
		taken[key] = fmt.Sprintf("relay line %d of this node", i+1)
	}
	return nil
}

// SetRouteLinks binds what runs after a node is deleted to clean up the tunnels
// only its routes by destination used. Left unbound, none is cleaned up.
//
// lock is the routing service's per-server lock. A delete takes it before any
// other lock (the order is: agent lock, routing lock, server locks, the state
// lock of the store), around reading the node's routes and deleting it.
func (s *Service) SetRouteLinks(after func(ctx context.Context, node *domain.Node) error, lock func(panelID int64) func()) {
	s.afterDelete, s.routeLock = after, lock
}

// lockRoutes takes the routing lock of a server; without one it takes none.
func (s *Service) lockRoutes(panelID int64) func() {
	if s.routeLock == nil {
		return func() {}
	}
	return s.routeLock(panelID)
}

// deleteNode deletes a node, and then the tunnels only its routes used. A caller
// that holds a server lock must not use it: see removeNode.
func (s *Service) deleteNode(ctx context.Context, id int64) error {
	if s.routeLock != nil {
		if n, err := s.nodes.GetByID(ctx, id); err == nil {
			defer s.lockRoutes(n.PanelID)()
		}
	}
	routed, err := s.removeNode(ctx, id)
	if err != nil {
		return err
	}
	s.afterRemove(ctx, routed)
	return nil
}

// removeNode deletes a node and returns it when it had routes, so that the
// cleanup of their tunnels (afterRemove), which takes server locks, can run once
// the caller holds none: those locks are not reentrant.
func (s *Service) removeNode(ctx context.Context, id int64) (*domain.Node, error) {
	var routed *domain.Node
	if s.afterDelete != nil {
		if n, err := s.nodes.GetByID(ctx, id); err == nil && len(n.DestRoutes) > 0 {
			routed = n
		}
	}
	if err := s.nodes.Delete(ctx, id); err != nil {
		return nil, err
	}
	return routed, nil
}

// afterRemove removes the tunnels only a deleted node's routes used. A failure
// is logged: the node is gone, and an unused tunnel is harmless.
func (s *Service) afterRemove(ctx context.Context, routed *domain.Node) {
	if routed == nil || s.afterDelete == nil {
		return
	}
	if err := s.afterDelete(ctx, routed); err != nil {
		log.Warn("node deleted, but the tunnels only its routes used were not removed", "node_id", routed.ID, "err", err)
	}
}
