package sqlstore

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func (r *DestAuditRepo) ReadDestinationUserHits(ctx context.Context, userID int64, since, until time.Time) (domain.DestRecentHits, error) {
	q, err := normalizeHitQuery(domain.DestHitQuery{UserID: userID, Since: since, Until: until})
	if err != nil || userID <= 0 {
		return domain.DestRecentHits{}, domain.ErrValidation
	}
	out := domain.DestRecentHits{Items: []domain.DestUserHit{}, ClientPanelIDs: []int64{}, Losses: domain.DestAuditLosses{Scope: "panel"}}
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err = r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&pspClientRow{}).Where("user_id = ? AND panel_id > 0", userID).Distinct("panel_id").Order("panel_id").Pluck("panel_id", &out.ClientPanelIDs).Error; err != nil {
			return err
		}
		if err := readUserHitItems(tx, q, &out.Items); err != nil {
			return err
		}
		return readHitLosses(tx, q, &out.Losses)
	}, options)
	if err != nil {
		return domain.DestRecentHits{}, auditStorageError(err)
	}
	return out, nil
}

type userHitKey struct{ source, action string }
type userHitDestKey struct {
	dest string
	port int
}
type userHitDest struct {
	value domain.DestUserHitDestination
	last  int64
}
type userHitGroup struct {
	value  domain.DestUserHit
	dests  map[userHitDestKey]*userHitDest
	panels map[int64]struct{}
}

func readUserHitItems(tx *gorm.DB, q domain.DestHitQuery, out *[]domain.DestUserHit) error {
	// Stream only this account's narrow rows. Fold repeated hours/nodes before
	// choosing the top three; limiting raw rows would silently lose counts.
	rows, err := hitFilteredScope(tx, q).Select("panel_id", "source", "action", "dest", "port", "count", "last_at").Rows()
	if err != nil {
		return err
	}
	groups := map[userHitKey]*userHitGroup{}
	for rows.Next() {
		var panel, count int64
		var source, action, dest string
		var port int
		var last time.Time
		if err := rows.Scan(&panel, &source, &action, &dest, &port, &count, &last); err != nil {
			rows.Close()
			return err
		}
		if _, _, ok := hitSourceID(source); !ok || panel <= 0 || count < 0 || (action != "block" && action != "observe") || dest == "" || port < 0 || port > 65535 || last.UnixMilli() <= 0 {
			rows.Close()
			return domain.ErrUnavailable
		}
		key := userHitKey{source, action}
		group := groups[key]
		if group == nil {
			group = &userHitGroup{value: domain.DestUserHit{Source: source, Action: action, TopDests: []domain.DestUserHitDestination{}, Panels: []domain.DestUserHitPanel{}}, dests: map[userHitDestKey]*userHitDest{}, panels: map[int64]struct{}{}}
			groups[key] = group
		}
		group.value.Count = auditReadAdd(group.value.Count, count)
		group.value.LastAt = max(group.value.LastAt, last.UnixMilli())
		group.panels[panel] = struct{}{}
		dkey := userHitDestKey{dest, port}
		d := group.dests[dkey]
		if d == nil {
			d = &userHitDest{value: domain.DestUserHitDestination{Dest: dest, Port: port}}
			group.dests[dkey] = d
		}
		d.value.Count = auditReadAdd(d.value.Count, count)
		d.last = max(d.last, last.UnixMilli())
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	policyIDs, groupIDs, panelIDs := []int64{}, []int64{}, []int64{}
	for key, group := range groups {
		kind, id, _ := hitSourceID(key.source)
		if kind == 'p' {
			policyIDs = append(policyIDs, id)
		} else {
			groupIDs = append(groupIDs, id)
		}
		for panel := range group.panels {
			panelIDs = append(panelIDs, panel)
		}
	}
	policies, err := readHitDisplayNames(tx, &destPolicyRow{}, "name", policyIDs)
	if err != nil {
		return err
	}
	groupNames, err := readHitDisplayNames(tx, &groupRow{}, "name", groupIDs)
	if err != nil {
		return err
	}
	panels, err := readHitDisplayNames(tx, &xuiPanelRow{}, "name", panelIDs)
	if err != nil {
		return err
	}
	for key, group := range groups {
		kind, id, _ := hitSourceID(key.source)
		names := policies
		if kind == 'g' {
			names = groupNames
		}
		group.value.SourceName = hitName(names, id)
		dests := make([]userHitDest, 0, len(group.dests))
		for _, d := range group.dests {
			dests = append(dests, *d)
		}
		slices.SortFunc(dests, func(a, b userHitDest) int {
			return cmp.Or(cmp.Compare(b.value.Count, a.value.Count), cmp.Compare(b.last, a.last), cmp.Compare(a.value.Dest, b.value.Dest), cmp.Compare(a.value.Port, b.value.Port))
		})
		for _, d := range dests[:min(3, len(dests))] {
			group.value.TopDests = append(group.value.TopDests, d.value)
		}
		for panel := range group.panels {
			group.value.Panels = append(group.value.Panels, domain.DestUserHitPanel{PanelID: panel, Name: hitName(panels, panel)})
		}
		slices.SortFunc(group.value.Panels, func(a, b domain.DestUserHitPanel) int { return cmp.Compare(a.PanelID, b.PanelID) })
		*out = append(*out, group.value)
	}
	slices.SortFunc(*out, func(a, b domain.DestUserHit) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(b.LastAt, a.LastAt), cmp.Compare(a.Source, b.Source), cmp.Compare(a.Action, b.Action))
	})
	return nil
}

var _ ports.DestUserHitReadRepo = (*DestAuditRepo)(nil)
