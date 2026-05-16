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
)

// InboundCleaner is the narrow subset of sync.Service used by node deletion.
// Defined here so the node package never imports sync.
type InboundCleaner interface {
	DelAllOwnedForInbound(ctx context.Context, panelID int64, inboundID int) error
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
		protocol domain.Protocol, userUUID, email, flow string, expireTime, totalGB int64) error
}

type Service struct {
	nodes    ports.NodeRepo
	pool     ports.XUIPool
	cleaner  InboundCleaner
	tasks    ports.SyncTaskRepo
	groups   ports.GroupRepo
	users    ports.UserRepo
	syncer   ClientSyncer
	settings ports.SettingsRepo
}

func New(nodes ports.NodeRepo, pool ports.XUIPool, cleaner InboundCleaner,
	tasks ports.SyncTaskRepo, groups ports.GroupRepo, users ports.UserRepo, syncer ClientSyncer, settings ports.SettingsRepo) *Service {
	return &Service{
		nodes:    nodes,
		pool:     pool,
		cleaner:  cleaner,
		tasks:    tasks,
		groups:   groups,
		users:    users,
		syncer:   syncer,
		settings: settings,
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

func (s *Service) GetInboundConfig(ctx context.Context, id int64) (*ports.Inbound, error) {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return nil, err
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

// ImportExisting registers an inbound that already lives in 3X-UI under
// panel management. No 3X-UI inbound-level write happens; only metadata is
// persisted, and clients are synced for any matching groups so newly
// added users immediately see this node in their subscriptions.
func (s *Service) ImportExisting(ctx context.Context, n *domain.Node) error {
	if n.DisplayName == "" || n.Region == "" {
		return fmt.Errorf("%w: display_name and region required", domain.ErrValidation)
	}
	s.fillPanelName(n)
	if existing, err := s.nodes.GetByPanelInbound(ctx, n.PanelID, n.InboundID); err == nil && existing != nil {
		return domain.ErrAlreadyExists
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return err
	}
	if _, err := c.GetInbound(ctx, n.InboundID); err != nil {
		return fmt.Errorf("inbound %d not found on panel %d: %w", n.InboundID, n.PanelID, err)
	}
	n.Enabled = true
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
	s.fillPanelName(n)
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
	if err := s.nodes.Create(ctx, n); err != nil {
		_ = c.DelInbound(context.Background(), inboundID)
		return err
	}
	if err := s.syncExistingUsersToNode(ctx, n); err != nil {
		log.Warn("sync users on create", "node_id", n.ID, "err", err)
	}
	return nil
}

func (s *Service) fillPanelName(n *domain.Node) {
	if n.PanelName != "" {
		return
	}
	for _, p := range s.pool.List() {
		if p.ID == n.PanelID {
			n.PanelName = p.Name
			return
		}
	}
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

// DetachAndSync stops managing a node without deleting the upstream inbound.
// Panel-managed clients (those tracked in the ownership table) get removed
// from 3X-UI; the inbound itself and any unmanaged clients are left intact.
// The node record on the panel side is dropped so subscriptions stop rendering
// it. No EnsureInboundDeletable preflight is needed — we are not deleting
// the inbound, so unmanaged clients don't block the operation.
func (s *Service) DetachAndSync(ctx context.Context, id int64) error {
	n, err := s.nodes.GetByID(ctx, id)
	if err != nil {
		return err
	}
	n.Enabled = false
	if err := s.nodes.Update(ctx, n); err != nil {
		return err
	}
	return s.enqueueNodeTask(ctx, domain.SyncTaskNodeDetach, n, "detach node", nil)
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
			task.Type != domain.SyncTaskNodeDetach &&
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
	case domain.SyncTaskNodeDetach:
		// Remove only the clients we created. Inbound + unmanaged clients
		// stay on 3X-UI, available to whatever else is using them.
		if err := s.cleaner.DelAllOwnedForInbound(ctx, n.PanelID, n.InboundID); err != nil {
			return fmt.Errorf("clear owned clients: %w", err)
		}
		return s.nodes.Delete(ctx, n.ID)
	case domain.SyncTaskNodeSetEnabled:
		if err := c.SetInboundEnable(ctx, n.InboundID, n.Enabled); err != nil {
			return fmt.Errorf("xui setEnable: %w", err)
		}
		return nil
	case domain.SyncTaskNodeUpdate:
		var spec ports.InboundSpec
		if err := json.Unmarshal([]byte(task.Payload), &spec); err != nil {
			return err
		}
		return c.UpdateInbound(ctx, n.InboundID, spec)
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
	s.fillPanelName(&n)
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return err
	}
	inboundID, err := c.AddInbound(ctx, p.Spec)
	if err != nil {
		if permanentErr := permanentInboundCreateError(err); permanentErr != nil {
			return permanentErr
		}
		return fmt.Errorf("xui addInbound: %w", err)
	}
	n.InboundID = inboundID
	n.Enabled = true
	if err := s.nodes.Create(ctx, &n); err != nil {
		_ = c.DelInbound(context.Background(), inboundID)
		return err
	}
	if err := s.syncExistingUsersToNode(ctx, &n); err != nil {
		log.Warn("sync users on queued create", "node_id", n.ID, "err", err)
	}
	return nil
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
	if typ != domain.SyncTaskNodeUpdate {
		if _, err := s.tasks.GetActiveByTarget(ctx, typ, "node", n.ID); err == nil {
			return nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
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
				info.protocol, u.UUID, email, info.flow, expireTime, 0); err != nil {
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

func (s *Service) ListUnmanagedInbounds(ctx context.Context) ([]*UnmanagedInbound, error) {
	out := []*UnmanagedInbound{}
	for _, panel := range s.pool.List() {
		c, err := s.pool.Get(panel.ID)
		if err != nil {
			continue
		}
		inbounds, err := c.ListInbounds(ctx)
		if err != nil {
			return nil, fmt.Errorf("list inbounds for panel %d: %w", panel.ID, err)
		}
		for i := range inbounds {
			inb := &inbounds[i]
			_, err := s.nodes.GetByPanelInbound(ctx, panel.ID, inb.ID)
			if err == nil {
				continue
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return nil, err
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
				PanelID:     panel.ID,
				PanelName:   panel.Name,
				InboundID:   inb.ID,
				Protocol:    inb.Protocol,
				Port:        inb.Port,
				Remark:      inb.Remark,
				Enable:      inb.Enable,
				ClientCount: len(inb.ClientStats),
			})
		}
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
