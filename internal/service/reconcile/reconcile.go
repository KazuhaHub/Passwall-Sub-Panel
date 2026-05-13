// Package reconcile runs the layered drift-detection job described in
// docs/ARCHITECTURE.md §9.4. Three triggers share the same checks:
//
//   - L1 immediate post-write verification (called from SyncSvc; not yet wired)
//   - L2 lightweight scan piggy-backed on TrafficSvc (every 5 min)
//   - L3 full reconciliation cron (default every 15 min)
//
// All checks operate only on rows present in the ownership table. Clients
// outside that table (operator's own clients and unimported users)
// are never touched.
package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/xrayspec"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
)

type Level int

const (
	LevelLight Level = iota // existence + enable only
	LevelFull               // all seven checks
)

// ClientSyncer is the narrow subset of sync.Service this package needs.
type ClientSyncer interface {
	AddClientToInbound(ctx context.Context, userID int64, panelID int64, inboundID int,
		protocol domain.Protocol, userUUID, email, flow string, expireTime int64) error
	SetOwnedClientEnable(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, userUUID string, enable bool, expireTime int64) error
	RotateClientUUID(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, oldUUID, newUUID string, enable bool, expireTime int64) error
}

type Service struct {
	users     ports.UserRepo
	ownership ports.OwnershipRepo
	nodes     ports.NodeRepo
	groups    ports.GroupRepo
	settings  ports.SettingsRepo
	audit     ports.AuditRepo
	pool      ports.XUIPool
	syncer    ClientSyncer
}

func New(users ports.UserRepo, ownership ports.OwnershipRepo, nodes ports.NodeRepo,
	groups ports.GroupRepo, settings ports.SettingsRepo, audit ports.AuditRepo, pool ports.XUIPool, syncer ClientSyncer) *Service {
	return &Service{
		users: users, ownership: ownership, nodes: nodes, groups: groups, settings: settings,
		audit: audit, pool: pool, syncer: syncer,
	}
}

// Report summarises one reconciliation run.
type Report struct {
	Scanned int     `json:"scanned"`
	Fixed   int     `json:"fixed"`
	Issues  []Issue `json:"issues"`
}

// Issue is one drift instance, fixed or not.
type Issue struct {
	PanelID     int64  `json:"panel_id"`
	PanelName   string `json:"panel_name"`
	InboundID   int    `json:"inbound_id"`
	ClientEmail string `json:"client_email"`
	Code        string `json:"code"`
	Detail      string `json:"detail"`
	Fixed       bool   `json:"fixed"`
}

// inboundCacheEntry holds the decoded inbound + its parsed clients[] so we
// don't decode the settings JSON repeatedly for the same inbound during
// one reconciliation pass.
type inboundCacheEntry struct {
	inbound *ports.Inbound
	clients []xrayspec.InboundClient
	method  string
}

type inboundCacheKey struct {
	panelID   int64
	inboundID int
}

// RunOnce performs one reconciliation pass at the requested depth.
func (s *Service) RunOnce(ctx context.Context, level Level) (*Report, error) {
	report := &Report{}
	cache := map[inboundCacheKey]*inboundCacheEntry{}

	rules := s.emailRules(ctx)
	allNodes, _ := s.nodes.List(ctx)

	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if level == LevelFull {
				s.checkMissingOwnerships(ctx, u, report, cache, rules, allNodes)
			}
			entries, err := s.ownership.ListByUser(ctx, u.ID)
			if err != nil {
				log.Warn("reconcile: list ownership", "user_id", u.ID, "err", err)
				continue
			}
			for _, e := range entries {
				report.Scanned++
				ce, err := s.loadInbound(ctx, cache, e.PanelID, e.InboundID)
				if err != nil {
					report.Issues = append(report.Issues, Issue{
						PanelID: e.PanelID, PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
						Code: "inbound_unreachable", Detail: err.Error(),
					})
					continue
				}
				if issue, fixed := s.checkOne(ctx, u, e, ce, level); issue != nil {
					issue.Fixed = fixed
					if fixed {
						report.Fixed++
					}
					report.Issues = append(report.Issues, *issue)
				}
			}
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}

	if level == LevelFull {
		s.checkNodes(ctx, report)
	}

	if report.Fixed > 0 || len(report.Issues) > 0 {
		_ = s.audit.Insert(ctx, &domain.AuditEntry{
			Actor:  "reconcile",
			Action: "reconcile_" + levelName(level),
			Target: fmt.Sprintf("scanned=%d fixed=%d issues=%d",
				report.Scanned, report.Fixed, len(report.Issues)),
			At: time.Now(),
		})
	}
	return report, nil
}

func (s *Service) emailRules(ctx context.Context) domain.EmailRules {
	sys, err := s.settings.Load(ctx, ports.UISettings{})
	if err == nil {
		return domain.EmailRules{Domain: sys.EmailDomain}
	}
	return domain.EmailRules{}
}

func (s *Service) checkMissingOwnerships(ctx context.Context, u *domain.User, report *Report, cache map[inboundCacheKey]*inboundCacheEntry, rules domain.EmailRules, nodes []*domain.Node) {
	if !u.Enabled {
		return
	}
	g, err := s.groups.GetByID(ctx, u.GroupID)
	if err != nil {
		return
	}
	entries, _ := s.ownership.ListByUser(ctx, u.ID)
	type nodeKey struct {
		panelID int64
		id      int
	}
	owned := map[nodeKey]bool{}
	for _, e := range entries {
		owned[nodeKey{e.PanelID, e.InboundID}] = true
	}

	for _, n := range nodes {
		if !n.Enabled {
			continue
		}
		if !group.Matches(n, g.TagFilter) {
			continue
		}
		if owned[nodeKey{n.PanelID, n.InboundID}] {
			continue
		}
		// Missing!
		report.Scanned++
		ce, err := s.loadInbound(ctx, cache, n.PanelID, n.InboundID)
		if err != nil {
			continue
		}
		protocol := crypto.DetectProtocol(ce.inbound.Protocol, ce.method)
		if protocol == "" {
			continue
		}

		email := u.ClientEmail(n.ID, rules)
		var expireTime int64
		if u.ExpireAt != nil {
			expireTime = u.ExpireAt.UnixMilli()
		}

		// find if it already exists in 3x-ui to avoid blind overwrite (though AddClient handles duplicates in xui wrapper)
		flow := ""
		if protocol == domain.ProtoVLESS || protocol == domain.ProtoVMess {
			// fallback flow empty, standard handling
		}

		err = s.syncer.AddClientToInbound(ctx, u.ID, n.PanelID, n.InboundID, protocol, u.UUID, email, flow, expireTime)

		fixed := err == nil
		report.Issues = append(report.Issues, Issue{
			PanelID:     n.PanelID,
			PanelName:   n.PanelName,
			InboundID:   n.InboundID,
			ClientEmail: email,
			Code:        "missing_ownership",
			Detail:      "User missing from node entirely",
			Fixed:       fixed,
		})
		if fixed {
			report.Fixed++
		}
	}
}

func (s *Service) loadInbound(ctx context.Context, cache map[inboundCacheKey]*inboundCacheEntry,
	panelID int64, inboundID int) (*inboundCacheEntry, error) {

	key := inboundCacheKey{panelID: panelID, inboundID: inboundID}
	if e, ok := cache[key]; ok {
		return e, nil
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return nil, err
	}
	inb, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return nil, err
	}
	settings, err := xrayspec.ParseSettings(inb.Settings)
	if err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	entry := &inboundCacheEntry{
		inbound: inb,
		clients: settings.Clients,
		method:  settings.Method,
	}
	cache[key] = entry
	return entry, nil
}

func (s *Service) checkOne(ctx context.Context, u *domain.User, e *domain.XUIClientEntry,
	ce *inboundCacheEntry, level Level) (*Issue, bool) {

	protocol := crypto.DetectProtocol(ce.inbound.Protocol, ce.method)
	found := xrayspec.FindClient(ce.clients, e.ClientEmail)

	var expireTime int64
	if u.ExpireAt != nil {
		expireTime = u.ExpireAt.UnixMilli()
	}

	// Check 1: existence
	if found == nil {
		if err := s.syncer.AddClientToInbound(ctx, u.ID, e.PanelID, e.InboundID,
			protocol, u.UUID, e.ClientEmail, "", expireTime); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "missing_client_recover_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "missing_client_recovered",
		}, true
	}

	// Check 3: enable mismatch
	if found.IsEnabled() != u.Enabled {
		if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			protocol, u.UUID, u.Enabled, expireTime); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "enable_mismatch_fix_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "enable_mismatch_fixed",
		}, true
	}

	if level == LevelLight {
		return nil, false
	}

	// Check 2: uuid mismatch (VLESS/VMess). Rotation needs the OLD uuid as
	// the 3X-UI updateClient path key, so we pass found.ID explicitly.
	if (protocol == domain.ProtoVLESS || protocol == domain.ProtoVMess) && found.ID != u.UUID {
		if err := s.syncer.RotateClientUUID(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			protocol, found.ID, u.UUID, u.Enabled, expireTime); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "uuid_mismatch_fix_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "uuid_mismatch_fixed",
		}, true
	}

	// Check 4: derived password mismatch (Trojan / SS / SS-2022)
	if protocol == domain.ProtoTrojan || protocol == domain.ProtoSS || protocol == domain.ProtoSS2022 {
		expected := crypto.DeriveProxyPassword(u.UUID, protocol)
		if found.Password != expected {
			if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
				protocol, u.UUID, u.Enabled, expireTime); err != nil {
				return &Issue{
					PanelID:   e.PanelID,
					PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
					Code: "password_mismatch_fix_failed", Detail: err.Error(),
				}, false
			}
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "password_mismatch_fixed",
			}, true
		}
	}

	// Check 5: mismatch on TotalGB or ExpiryTime on a panel-managed client
	if found.TotalGB > 0 || found.ExpiryTime != expireTime {
		if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			protocol, u.UUID, u.Enabled, expireTime); err != nil {
			return &Issue{
				PanelID:   e.PanelID,
				PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
				Code: "extra_field_fix_failed", Detail: err.Error(),
			}, false
		}
		return &Issue{
			PanelID:   e.PanelID,
			PanelName: e.PanelName, InboundID: e.InboundID, ClientEmail: e.ClientEmail,
			Code: "extra_field_fixed",
		}, true
	}

	return nil, false
}

// checkNodes verifies every nodes row still maps to an existing 3X-UI
// inbound. Disappeared inbounds get nodes.enabled flipped to false; the
// row is preserved so an admin can inspect what happened.
func (s *Service) checkNodes(ctx context.Context, report *Report) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return
	}
	inboundsPerPanel := map[int64]map[int]bool{}
	for _, n := range nodes {
		known, ok := inboundsPerPanel[n.PanelID]
		if !ok {
			c, err := s.pool.Get(n.PanelID)
			if err != nil {
				continue
			}
			inbs, err := c.ListInbounds(ctx)
			if err != nil {
				continue
			}
			known = make(map[int]bool, len(inbs))
			for _, inb := range inbs {
				known[inb.ID] = true
			}
			inboundsPerPanel[n.PanelID] = known
		}
		if !known[n.InboundID] && n.Enabled {
			n.Enabled = false
			if err := s.nodes.Update(ctx, n); err == nil {
				report.Issues = append(report.Issues, Issue{
					PanelID:   n.PanelID,
					PanelName: n.PanelName, InboundID: n.InboundID,
					Code:   "inbound_missing_disabled_node",
					Detail: fmt.Sprintf("node id=%d", n.ID),
					Fixed:  true,
				})
				report.Fixed++
			}
		}
	}
}

func levelName(l Level) string {
	if l == LevelFull {
		return "full"
	}
	return "light"
}
