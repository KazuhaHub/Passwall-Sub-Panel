package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// transitRouteRow is a node's routes by destination (transit.route.v1): the
// ordered list the administrator wrote, as one JSON document because it is
// written and read as a whole and never queried into, and the digests of what
// was sent to the node's server and what that server confirmed.
//
// It is a table of its own and not a column of nodes. The node row is rewritten
// whole by several paths that load it first (imports, the poll writers); a
// column there would be rolled back by any of them, and the sent and applied
// digests are written by the sync service on every round.
//
// SENT AND APPLIED ARE BOTH KEPT, like a link's. "Sent" is the digest of the
// listener's compiled routes in the config last handed to the server; "applied"
// is the digest of the routes in the config the server last confirmed with this
// listener applied. The routes run when the two are equal and not empty; a
// confirmation of older content never counts for what was sent after it.
type transitRouteRow struct {
	NodeID int64 `gorm:"primaryKey;autoIncrement:false"`
	// Body is the routes as JSON, bytes and not TEXT: a route may hold thousands
	// of typed entries, past MySQL's 64 KiB TEXT. Nullable, so the column can be
	// added to a table that has rows (it replaced the TEXT column spec, which
	// widenRouteSpec carries over and drops).
	Body    []byte
	Sent    string `gorm:"size:64;not null;default:''"`
	Applied string `gorm:"size:64;not null;default:''"`
	// Withheld is the reason the node's listener is not being sent, "" while it is.
	Withheld  string `gorm:"size:32;not null;default:''"`
	SentAt    *time.Time
	AppliedAt *time.Time
	UpdatedAt time.Time
}

func (transitRouteRow) TableName() string { return "transit_routes" }

// transitRouteListRow is the last good expansion of a list a route names: the
// literal entries it last gave when it had something a route could use. A route
// keeps compiling from it while an automatic refresh leaves the list empty or
// unavailable, so those destinations do not fall through to the default exit;
// only an administrator's confirmed edit forgets it. One row per list, shared by
// the routes that name it. The entries are bytes, one per line: a list can hold
// tens of thousands, past MySQL's 64 KiB TEXT.
type transitRouteListRow struct {
	ListID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Domains   []byte `gorm:"not null"`
	CIDRs     []byte `gorm:"column:cidrs;not null"`
	Digest    string `gorm:"size:64;not null;default:''"`
	UpdatedAt time.Time
}

func (transitRouteListRow) TableName() string { return "transit_route_lists" }

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\n")
}

// GoodList returns the last good expansion of a list.
func (r *transitRouteRepo) GoodList(ctx context.Context, listID int64) (domains, cidrs []string, found bool, err error) {
	var row transitRouteListRow
	if err := r.db.WithContext(ctx).Where("list_id = ?", listID).Limit(1).Find(&row).Error; err != nil {
		return nil, nil, false, err
	}
	if row.ListID == 0 {
		return nil, nil, false, nil
	}
	return splitLines(row.Domains), splitLines(row.CIDRs), true, nil
}

// SaveGoodList stores a list's expansion as its last good one; it writes only
// when the digest differs from the stored one.
func (r *transitRouteRepo) SaveGoodList(ctx context.Context, listID int64, digest string, domains, cidrs []string) error {
	row := transitRouteListRow{ListID: listID, Domains: []byte(strings.Join(domains, "\n")), CIDRs: []byte(strings.Join(cidrs, "\n")), Digest: digest, UpdatedAt: time.Now().UTC()}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Under the state lock, and only while some route still names the list: a
		// save that raced a route letting go of it must not bring its content back
		// after the prune that went with the release.
		if err := lockDestState(tx); err != nil {
			return err
		}
		held, err := listRoutes(ctx, tx)
		if err != nil {
			return err
		}
		named := false
		for _, item := range held {
			for _, route := range item.Routes {
				named = named || slices.Contains(route.ListIDs, listID)
			}
		}
		if !named {
			return nil
		}
		var have transitRouteListRow
		if err := tx.Select("list_id", "digest").Where("list_id = ?", listID).Limit(1).Find(&have).Error; err != nil {
			return err
		}
		if have.ListID != 0 && have.Digest == digest {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "list_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"domains", "cidrs", "digest", "updated_at"}),
		}).Create(&row).Error
	})
}

// ForgetGoodList drops a list's last good expansion: the administrator confirmed
// an edit that leaves routes with nothing from it.
func (r *transitRouteRepo) ForgetGoodList(ctx context.Context, listID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		return tx.Where("list_id = ?", listID).Delete(&transitRouteListRow{}).Error
	})
}

func (r transitRouteRow) toDomain() (domain.NodeRoutes, error) {
	var routes []domain.DestRoute
	if len(r.Body) > 0 {
		if err := json.Unmarshal(r.Body, &routes); err != nil {
			return domain.NodeRoutes{}, fmt.Errorf("routes of node %d: %w", r.NodeID, err)
		}
	}
	return domain.NodeRoutes{NodeID: r.NodeID, Routes: routes, Sent: r.Sent, Applied: r.Applied, Withheld: r.Withheld, SentAt: r.SentAt, AppliedAt: r.AppliedAt}, nil
}

type transitRouteRepo struct{ db *gorm.DB }

// NewTransitRouteRepo returns the routes store.
func NewTransitRouteRepo(db *gorm.DB) *transitRouteRepo { return &transitRouteRepo{db: db} }

// Get returns a node's routes; a node that has none is an empty value, not an
// error.
func (r *transitRouteRepo) Get(ctx context.Context, nodeID int64) (domain.NodeRoutes, error) {
	var row transitRouteRow
	err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Limit(1).Find(&row).Error
	if err != nil {
		return domain.NodeRoutes{}, err
	}
	if row.NodeID == 0 {
		return domain.NodeRoutes{NodeID: nodeID}, nil
	}
	return row.toDomain()
}

// List returns every node's routes, by node id. Nodes with an empty list are
// left out.
func (r *transitRouteRepo) List(ctx context.Context) ([]domain.NodeRoutes, error) {
	return listRoutes(ctx, r.db)
}

func listRoutes(ctx context.Context, db *gorm.DB) ([]domain.NodeRoutes, error) {
	var rows []transitRouteRow
	if err := db.WithContext(ctx).Order("node_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.NodeRoutes, 0, len(rows))
	for _, row := range rows {
		item, err := row.toDomain()
		if err != nil {
			return nil, err
		}
		if len(item.Routes) == 0 {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// Replace stores a node's routes whole. The delivery digests are kept: the next
// sync sends the new content, which moves "sent", and "applied" follows when the
// server confirms it.
func (r *transitRouteRepo) Replace(ctx context.Context, nodeID int64, routes []domain.DestRoute) error {
	spec, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	if routes == nil {
		spec = []byte("[]")
	}
	row := transitRouteRow{NodeID: nodeID, Body: spec, UpdatedAt: time.Now().UTC()}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// FIRST the lock a list's deletion takes (lockDestState), always, so that
		// every writer of routes takes the locks in the one order (see the lock
		// order at lockDestState): a list named here exists, and cannot be deleted
		// until these routes let go of it, and the kept list content pruned below
		// is not written to at the same time.
		if err := lockDestState(tx); err != nil {
			return err
		}
		// And the node must still exist: routes saved while the node was being
		// deleted would be an orphan row nothing removes, that blocks the deletion
		// of the lists it names. (Clearing needs no node.)
		if len(routes) > 0 {
			var nodes int64
			if err := tx.Model(&nodeRow{}).Where("id = ?", nodeID).Count(&nodes).Error; err != nil {
				return err
			}
			if nodes == 0 {
				return fmt.Errorf("%w: the node no longer exists", domain.ErrNotFound)
			}
		}
		var wanted []int64
		for _, route := range routes {
			wanted = append(wanted, route.ListIDs...)
		}
		if len(wanted) > 0 {
			slices.Sort(wanted)
			wanted = slices.Compact(wanted)
			var found int64
			if err := tx.Model(&destListRow{}).Where("id IN ?", wanted).Count(&found).Error; err != nil {
				return err
			}
			if int(found) != len(wanted) {
				return fmt.Errorf("%w: a list named by the routes no longer exists", domain.ErrNotFound)
			}
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "node_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"body", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
		return pruneGoodLists(ctx, tx)
	})
}

// pruneGoodLists drops the kept content of every list no route names any more
// (and of lists that no longer exist): it must not come back if a route names
// the list again later, when the list may hold something else.
func pruneGoodLists(ctx context.Context, tx *gorm.DB) error {
	held, err := listRoutes(ctx, tx)
	if err != nil {
		return err
	}
	named := map[int64]bool{}
	for _, item := range held {
		for _, route := range item.Routes {
			for _, id := range route.ListIDs {
				named[id] = true
			}
		}
	}
	var kept []int64
	if err := tx.Model(&transitRouteListRow{}).Pluck("list_id", &kept).Error; err != nil {
		return err
	}
	var drop []int64
	for _, id := range kept {
		if !named[id] {
			drop = append(drop, id)
		}
	}
	if len(drop) == 0 {
		return nil
	}
	return tx.Where("list_id IN ?", drop).Delete(&transitRouteListRow{}).Error
}

// RecordSent stores the digest of the routes put in the node's config ("" when
// none were sent) and why the node is withheld ("" when it is not). It writes
// only on a change and reports whether it did.
func (r *transitRouteRepo) RecordSent(ctx context.Context, nodeID int64, digest, withheld string, at time.Time) (bool, error) {
	update := map[string]any{"sent": digest, "withheld": withheld}
	if digest == "" {
		// Routes that are not being sent are not running, and a confirmation of
		// older content must not come back to life if they are sent again.
		update["sent_at"], update["applied"], update["applied_at"] = nil, "", nil
	} else {
		update["sent_at"] = at
	}
	result := r.db.WithContext(ctx).Model(&transitRouteRow{}).Where("node_id = ? AND (sent <> ? OR withheld <> ?)", nodeID, digest, withheld).Updates(update)
	return result.RowsAffected > 0, result.Error
}

// SetApplied records the digest the server confirmed ("" withdraws it). It
// reports whether the stored value changed.
func (r *transitRouteRepo) SetApplied(ctx context.Context, nodeID int64, digest string, at time.Time) (bool, error) {
	update := map[string]any{"applied": digest}
	if digest == "" {
		update["applied_at"] = nil
	} else {
		update["applied_at"] = at
	}
	result := r.db.WithContext(ctx).Model(&transitRouteRow{}).Where("node_id = ? AND applied <> ?", nodeID, digest).Updates(update)
	return result.RowsAffected > 0, result.Error
}

// deleteNodeRoutesTx removes a deleted node's routes in the node's own
// transaction: they are keyed by node id with no foreign key.
func deleteNodeRoutesTx(tx *gorm.DB, nodeID int64) error {
	if err := tx.Where("node_id = ?", nodeID).Delete(&transitRouteRow{}).Error; err != nil {
		return err
	}
	return pruneGoodLists(context.Background(), tx)
}

// attachRouteState fills Node.DestRoutes, RoutesSent and RoutesApplied.
func attachRouteState(ctx context.Context, db *gorm.DB, nodes []*domain.Node) error {
	for _, node := range nodes {
		if node != nil {
			node.DestRoutes, node.RoutesSent, node.RoutesApplied, node.RoutesWithheld = nil, "", "", ""
		}
	}
	if len(nodes) == 0 {
		return nil
	}
	var rows []transitRouteRow
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return fmt.Errorf("load routes: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	byNode := make(map[int64]transitRouteRow, len(rows))
	for _, row := range rows {
		byNode[row.NodeID] = row
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		row, ok := byNode[node.ID]
		if !ok {
			continue
		}
		item, err := row.toDomain()
		if err != nil {
			return err
		}
		node.DestRoutes, node.RoutesSent, node.RoutesApplied, node.RoutesWithheld = item.Routes, item.Sent, item.Applied, item.Withheld
	}
	return nil
}
