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
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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

// ClientSyncer is the narrow subset of sync.Service used when syncing users
// onto a newly registered node.
type ClientSyncer interface {
	// totalGB is the per-client traffic floor for 3X-UI (0 = unlimited).
	// node.Service doesn't have a TrafficUsageReader to compute the real
	// floor, so callers pass 0; the next traffic-poll cycle's
	// pushClientConfigToAll will set the correct value.
	AddClientToInbound(ctx context.Context, userID int64, panelID int64, inboundID int,
		protocol domain.Protocol, ssMethod, userUUID, email, flow string, expireTime, totalGB int64) error
}

type Service struct {
	nodes      ports.NodeRepo
	separators ports.SeparatorRepo
	pool       ports.XUIPool
	cleaner    InboundCleaner
	tasks      ports.SyncTaskRepo
	groups     ports.GroupRepo
	users      ports.UserRepo
	syncer     ClientSyncer
	settings   ports.SettingsRepo
}

func New(nodes ports.NodeRepo, separators ports.SeparatorRepo, pool ports.XUIPool, cleaner InboundCleaner,
	tasks ports.SyncTaskRepo, groups ports.GroupRepo, users ports.UserRepo, syncer ClientSyncer, settings ports.SettingsRepo) *Service {
	return &Service{
		nodes:      nodes,
		separators: separators,
		pool:       pool,
		cleaner:    cleaner,
		tasks:      tasks,
		groups:     groups,
		users:      users,
		syncer:     syncer,
		settings:   settings,
	}
}

func (s *Service) emailRules(ctx context.Context) domain.EmailRules {
	defaults := ports.UISettings{EmailDomain: "psp.local"}
	st, err := s.settings.Load(ctx, defaults)
	if err != nil {
		st = defaults
	}
	if st.EmailDomain == "" {
		st.EmailDomain = "psp.local"
	}
	return domain.EmailRules{Domain: st.EmailDomain}
}

// ---- Read ----

func (s *Service) Get(ctx context.Context, id int64) (*domain.Node, error) {
	return s.nodes.GetByID(ctx, id)
}

// GetInboundConfig returns the inbound's connection config for the admin edit
// dialog. v3.5: PSP owns the config, so serve the local snapshot when present —
// the same source render and reconcile treat as truth, so the edit form, the
// rendered subscription and drift enforcement can't disagree (editing a node
// no longer silently adopts whatever an operator changed in 3X-UI). Falls back
// to a live fetch only for never-captured nodes (pre-v3.5 / freshly imported
// before the first reconcile backfill). The detail page's client list comes
// from ListClientsOfInbound (always live), so stripping clients here is fine.
func (s *Service) GetInboundConfig(ctx context.Context, id int64) (*ports.Inbound, error) {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if inboundcfg.HasLocalConfig(n) {
		return inboundcfg.InboundFromNode(n), nil
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, err
	}
	return c.GetInbound(ctx, n.InboundID)
}

func (s *Service) List(ctx context.Context) ([]*domain.Node, error) {
	return s.nodes.List(ctx)
}

// ---- Create flows ----

// ---- Separator CRUD --------------------------------------------------------
//
// Separators live in their own `nodes_separator` table (see
// internal/adapters/mysql/separator_repo.go) and are bound to groups by an
// explicit list, not by tag_filter. The pre-v3.0.0-beta.7 design that
// stashed them in `nodes` with kind='separator' + synthetic negative
// inbound_id is gone — legacy rows are dropped by cleanupLegacyState.
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
	// Edit dialog no longer surfaces sort_order; the absent field arrives
	// as 0 and must not clobber the position the admin set via drag. Load
	// the existing row and preserve it when the caller didn't specify one.
	if e.SortOrder <= 0 {
		existing, err := s.separators.GetByID(ctx, e.ID)
		if err != nil {
			return err
		}
		e.SortOrder = existing.SortOrder
	}
	return s.separators.Update(ctx, e)
}

// nextSortOrder returns max(sort_order across nodes + separators) + 10,
// or 10 if both tables are empty. Used to drop new separators at the tail
// of the merged list without forcing the admin to pick a number.
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
	// Import = take ownership: capture the live inbound's config into the local
	// snapshot so render reads it without a live fetch and reconcile can keep
	// 3X-UI aligned to PSP. clients[] is stripped (ownership-managed).
	inboundcfg.Capture(n, inb)
	if err := s.nodes.Create(ctx, n); err != nil {
		return err
	}
	if err := s.syncExistingUsersToNode(ctx, n); err != nil {
		log.Warn("sync users on import", "node_id", n.ID, "err", err)
	}
	return nil
}

// CreateInbound creates a brand new inbound in 3X-UI and registers it,
// then syncs clients for every panel user whose group would include
// this node. Admin doesn't need a separate "resync everyone" step.
func (s *Service) CreateInbound(ctx context.Context, n *domain.Node, spec ports.InboundSpec) error {
	if n.DisplayName == "" || n.Region == "" || n.PanelID == 0 {
		return fmt.Errorf("%w: display_name, region and panel_id required", domain.ErrValidation)
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return s.enqueueNodeCreateTask(ctx, n, spec, fmt.Errorf("xui panel: %w", err))
	}
	inboundID, err := c.AddInbound(ctx, spec)
	if err != nil {
		if permanentErr := permanentInboundCreateError(err); permanentErr != nil {
			return permanentErr
		}
		return s.enqueueNodeCreateTask(ctx, n, spec, fmt.Errorf("xui addInbound: %w", err))
	}
	n.InboundID = inboundID
	n.Enabled = true
	// v3.5 write-through: persist the just-pushed config into the local snapshot
	// so the node renders without a live fetch from its first subscription.
	inboundcfg.ApplySpec(n, spec)
	if err := s.nodes.Create(ctx, n); err != nil {
		_ = c.DelInbound(context.Background(), inboundID)
		return err
	}
	if err := s.syncExistingUsersToNode(ctx, n); err != nil {
		log.Warn("sync users on create", "node_id", n.ID, "err", err)
	}
	return nil
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
	return s.nodes.BatchUpdateSortOrder(ctx, updates)
}

func (s *Service) UpdateMetadata(ctx context.Context, n *domain.Node) error {
	if _, err := s.nodes.GetByID(ctx, n.ID); err != nil {
		return err
	}
	return s.nodes.Update(ctx, n)
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
	inboundcfg.ApplySpec(n, spec)
	if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
		return err
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return s.enqueueNodeTask(ctx, domain.SyncTaskNodeUpdate, n, "update node config", spec)
	}
	if err := c.UpdateInbound(ctx, n.InboundID, spec); err != nil {
		return s.enqueueNodeTask(ctx, domain.SyncTaskNodeUpdate, n, "update node config", spec)
	}
	return nil
}

func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	n.Enabled = enabled
	if err := s.nodes.Update(ctx, n); err != nil {
		return err
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		if taskErr := s.enqueueNodeTask(ctx, domain.SyncTaskNodeSetEnabled, n, "sync node enabled state", map[string]bool{"enabled": enabled}); taskErr != nil {
			log.Warn("enqueue node enabled sync failed", "node_id", n.ID, "err", taskErr)
		}
		return nil
	}
	if err := c.SetInboundEnable(ctx, n.InboundID, enabled); err != nil {
		if taskErr := s.enqueueNodeTask(ctx, domain.SyncTaskNodeSetEnabled, n, "sync node enabled state", map[string]bool{"enabled": enabled}); taskErr != nil {
			log.Warn("enqueue node enabled sync failed", "node_id", n.ID, "err", taskErr)
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
		return s.nodes.Delete(ctx, id)
	}
	if err := s.cleaner.EnsureInboundDeletable(ctx, n.PanelID, n.InboundID); err != nil {
		if errors.Is(err, domain.ErrInboundHasUnmanagedClients) {
			return err
		}
		// Remote reachability problems are left to the queued task. The task
		// will run the same guard before deleting any managed clients.
		log.Warn("node delete preflight failed; queueing guarded delete", "node_id", n.ID, "err", err)
	}
	n.Enabled = false
	if err := s.nodes.Update(ctx, n); err != nil {
		return err
	}
	return s.enqueueNodeTask(ctx, domain.SyncTaskNodeDelete, n, "delete node", nil)
}

// DetachAndSync drops the node record and the panel's ownership rows for
// this inbound without contacting 3X-UI. Intended for nodes whose upstream
// is already unreachable (server decommissioned, panel offline) where
// queueing a remote delete would just retry forever. Clients PSP previously
// created on the inbound remain in 3X-UI; the admin can clean them up
// there directly. Separators (layout-only rows) have no upstream binding,
// so detach falls back to a plain local delete.
func (s *Service) DetachAndSync(ctx context.Context, id int64) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if n.IsSeparator() {
		return s.nodes.Delete(ctx, id)
	}
	if err := s.cleaner.UnclaimAllForInbound(ctx, n.PanelID, n.InboundID); err != nil {
		return fmt.Errorf("unclaim owned clients: %w", err)
	}
	return s.nodes.Delete(ctx, n.ID)
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
		if err := s.tasks.MarkRunning(ctx, task.ID); err != nil {
			return err
		}
		if err := s.runNodeTask(ctx, task); err != nil {
			if isPermanentNodeTaskError(err) {
				if markErr := s.tasks.Cancel(ctx, task.ID); markErr != nil {
					return markErr
				}
				continue
			}
			next := time.Now().Add(nodeTaskBackoff(task.Attempts + 1))
			if markErr := s.tasks.MarkRetry(ctx, task.ID, err.Error(), next); markErr != nil {
				return markErr
			}
			continue
		}
		if err := s.tasks.MarkSucceeded(ctx, task.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) runNodeTask(ctx context.Context, task *domain.SyncTask) error {
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
		if err := s.cleaner.EnsureInboundDeletable(ctx, n.PanelID, n.InboundID); err != nil {
			return err
		}
		if err := s.cleaner.DelAllOwnedForInbound(ctx, n.PanelID, n.InboundID); err != nil {
			return fmt.Errorf("clear owned clients: %w", err)
		}
		if err := s.cleaner.DeleteInbound(ctx, n.PanelID, n.InboundID); err != nil {
			return err
		}
		return s.nodes.Delete(ctx, n.ID)
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
		// regress 3X-UI to a superseded spec.
		return c.UpdateInbound(ctx, n.InboundID, inboundcfg.SpecFromNode(n))
	default:
		return nil
	}
}

type nodeCreateTaskPayload struct {
	Node domain.Node       `json:"node"`
	Spec ports.InboundSpec `json:"spec"`
}

func (s *Service) runNodeCreateTask(ctx context.Context, task *domain.SyncTask) error {
	var p nodeCreateTaskPayload
	if err := json.Unmarshal([]byte(task.Payload), &p); err != nil {
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
				inboundcfg.Capture(&n, adopted)
				if err := s.nodes.Create(ctx, &n); err != nil {
					return err
				}
				if err := s.syncExistingUsersToNode(ctx, &n); err != nil {
					log.Warn("sync users on adopted-create", "node_id", n.ID, "err", err)
				}
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
	if err := s.syncExistingUsersToNode(ctx, &n); err != nil {
		log.Warn("sync users on queued create", "node_id", n.ID, "err", err)
	}
	return nil
}

// tryAdoptOrphan searches the panel's live inbound list for one matching
// `spec` (port + protocol + listen) that no other PSP node already owns,
// and returns it for adoption. Returns (nil, nil) if no match — caller
// should treat that as a genuine port conflict. Strict matching (not just
// port) keeps the false-adoption risk low when an operator happens to
// have a different inbound on the same port.
func (s *Service) tryAdoptOrphan(ctx context.Context, c ports.XUIClient, panelID int64, spec ports.InboundSpec) (*ports.Inbound, error) {
	inbs, err := c.ListInbounds(ctx)
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
		if existing, _ := s.nodes.GetByPanelInbound(ctx, panelID, inb.ID); existing != nil {
			// Another PSP node already claimed this inbound — likely a
			// concurrent create won. Don't double-adopt.
			continue
		}
		return inb, nil
	}
	return nil, nil
}

func isPortAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "port already exists")
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
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "port already exists"):
		return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, msg)
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

// nodeTaskBackoff returns a flat 1-minute retry interval — same rationale
// as deleteTaskBackoff in user.go.
func nodeTaskBackoff(_ int) time.Duration {
	return time.Minute
}

// ---- New-node user sync ----

// syncExistingUsersToNode walks every group; for groups whose tag_filter would
// include this node, every enabled member gets a client pushed via the
// ClientSyncer. Errors per user are logged and the loop continues — the
// reconciliation pass heals anything left behind.
func (s *Service) syncExistingUsersToNode(ctx context.Context, n *domain.Node) error {
	info, err := s.inspectInbound(ctx, n)
	if err != nil {
		return fmt.Errorf("inspect inbound: %w", err)
	}
	if info.protocol == "" {
		log.Warn("new-node sync skip: unsupported protocol", "node_id", n.ID)
		return nil
	}

	groups, err := s.groups.List(ctx)
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}

	rules := s.emailRules(ctx)
	pushed, considered := 0, 0
	for _, g := range groups {
		if !group.Matches(n, g.TagFilter) {
			continue
		}
		members, err := s.users.ListByGroup(ctx, g.ID)
		if err != nil {
			log.Warn("new-node sync list members", "group_id", g.ID, "err", err)
			continue
		}
		for _, u := range members {
			considered++
			if !u.Enabled {
				continue
			}
			email := u.ClientEmail(n.ID, rules)
			var expireTime int64
			if u.ExpireAt != nil {
				expireTime = u.ExpireAt.UnixMilli()
			}
			// floor 0 = unlimited on 3X-UI side; the next traffic-poll
			// cycle's pushClientConfigToAll will set the real floor (~5 min
			// after node creation). Adding TrafficUsageReader to node.Service
			// would invert the dependency graph for marginal benefit.
			if err := s.syncer.AddClientToInbound(ctx, u.ID, n.PanelID, n.InboundID,
				info.protocol, info.ssMethod, u.UUID, email, info.flow, expireTime, 0); err != nil {
				log.Warn("new-node sync add client",
					"user_id", u.ID, "node_id", n.ID, "err", err)
				continue
			}
			pushed++
		}
	}
	log.Info("synced existing users on node", "node_id", n.ID,
		"considered_members", considered, "pushed", pushed)
	return nil
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
	inbounds, err := c.ListInbounds(ctx)
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

func (s *Service) ListClientsOfInbound(ctx context.Context, nodeID int64, ownership ports.OwnershipRepo) ([]*InboundClientView, error) {
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
	out := make([]*InboundClientView, 0, len(inb.ClientStats))
	for _, cs := range inb.ClientStats {
		view := &InboundClientView{
			Email:      cs.Email,
			Up:         cs.Up,
			Down:       cs.Down,
			Enable:     cs.Enable,
			ExpiryTime: cs.ExpiryTime,
		}
		entry, err := ownership.GetByMatch(ctx, n.PanelID, n.InboundID, cs.Email)
		if err == nil {
			view.Managed = true
			view.OwnerUserID = entry.UserID
		}
		out = append(out, view)
	}
	return out, nil
}
