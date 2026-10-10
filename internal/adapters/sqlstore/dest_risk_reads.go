package sqlstore

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

// ReadDestinationRiskWindow never loads a destination, port, address or name.
// The policy selection, current clients, counts and related block losses all
// belong to one private read-only repeatable snapshot. No per-user SQL reads.
func (r *DestAuditRepo) ReadDestinationRiskWindow(ctx context.Context, since, until time.Time) (domain.DestRiskWindow, error) {
	if err := ctx.Err(); err != nil {
		return domain.DestRiskWindow{}, err
	}
	if since.IsZero() || since.UnixMilli() <= 0 || until.Sub(since) != domain.RiskDestBlockWindowHours*time.Hour {
		return domain.DestRiskWindow{}, domain.ErrValidation
	}
	out := domain.DestRiskWindow{Users: map[int64]domain.DestRiskUserWindow{}}
	var options *sql.TxOptions
	if r.db.Dialector.Name() != "sqlite" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		b := destRiskBuilder{users: map[int64]*destRiskUser{}, panelUsers: map[int64]map[int64]bool{}}
		if err := b.readClients(tx); err != nil {
			return err
		}
		if err := b.readCounts(tx, since, until); err != nil {
			return err
		}
		if err := b.readLosses(tx, since, until); err != nil {
			return err
		}
		for id, user := range b.users {
			row := domain.DestRiskUserWindow{Sources: []domain.DestBlockSource{}, ClientPanelIDs: []int64{}, Losses: user.losses}
			for source, count := range user.sources {
				row.Sources = append(row.Sources, domain.DestBlockSource{Source: source, Count: count})
			}
			slices.SortFunc(row.Sources, func(a, b domain.DestBlockSource) int { return cmp.Compare(a.Source, b.Source) })
			for panel := range user.clients {
				row.ClientPanelIDs = append(row.ClientPanelIDs, panel)
			}
			slices.Sort(row.ClientPanelIDs)
			out.Users[id] = row
		}
		return nil
	}, options)
	if err := ctx.Err(); err != nil {
		return domain.DestRiskWindow{}, err
	}
	if err != nil {
		return domain.DestRiskWindow{}, auditStorageError(err)
	}
	return out, nil
}

type destRiskUser struct {
	sources map[string]int64
	clients map[int64]bool
	losses  domain.DestAuditLosses
}

type destRiskBuilder struct {
	users      map[int64]*destRiskUser
	panelUsers map[int64]map[int64]bool
}

func (b *destRiskBuilder) user(id int64) *destRiskUser {
	if b.users[id] == nil {
		b.users[id] = &destRiskUser{sources: map[string]int64{}, clients: map[int64]bool{}, losses: domain.DestAuditLosses{Scope: "panel"}}
	}
	return b.users[id]
}

func (b *destRiskBuilder) relate(user, panel int64) {
	if b.panelUsers[panel] == nil {
		b.panelUsers[panel] = map[int64]bool{}
	}
	b.panelUsers[panel][user] = true
}

func (b *destRiskBuilder) readClients(tx *gorm.DB) error {
	rows, err := tx.Model(&pspClientRow{}).Select("psp_clients.user_id", "psp_clients.panel_id").Distinct().Joins("JOIN users ON users.id = psp_clients.user_id").Where("psp_clients.panel_id > 0").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var user, panel int64
		if err := rows.Scan(&user, &panel); err != nil {
			return err
		}
		if user <= 0 || panel <= 0 {
			return domain.ErrUnavailable
		}
		b.user(user).clients[panel] = true
		b.relate(user, panel)
	}
	return rows.Err()
}

func (b *destRiskBuilder) readCounts(tx *gorm.DB, since, until time.Time) error {
	var ids []int64
	if err := tx.Model(&destPolicyRow{}).Where("counts_as_risk = ?", true).Order("id").Pluck("id", &ids).Error; err != nil {
		return err
	}
	for start := 0; start < len(ids); start += auditStatementRows {
		sources := make([]string, 0, auditStatementRows)
		selected := make(map[string]bool, auditStatementRows)
		for _, id := range ids[start:min(start+auditStatementRows, len(ids))] {
			if id <= 0 {
				return domain.ErrUnavailable
			}
			sources = append(sources, "p"+strconv.FormatInt(id, 10))
			selected[sources[len(sources)-1]] = true
		}
		// An aggregate spanning the cutoff cannot be split into exact counts.
		// Only observations known entirely inside the requested 24 hours count;
		// this preserves a lower bound instead of flagging on an older bucket.
		rows, err := tx.Model(&destHitRow{}).Select("dest_hits.user_id", "dest_hits.panel_id", "dest_hits.source", "dest_hits.action", "dest_hits.count").Joins("JOIN users ON users.id = dest_hits.user_id").Where("dest_hits.source IN ? AND dest_hits.action = ? AND hour_ms >= ? AND hour_ms < ? AND first_at >= ? AND last_at <= ?", sources, "block", since.UTC().Truncate(time.Hour).UnixMilli(), until.UnixMilli(), since.UTC(), until.UTC()).Rows()
		if err != nil {
			return err
		}
		for rows.Next() {
			var user, panel, count int64
			var source, action string
			if err := rows.Scan(&user, &panel, &source, &action, &count); err != nil {
				rows.Close()
				return err
			}
			// MySQL's default string collation can match upper-case aliases.
			// Confirm exact canonical spelling before anything is counted.
			kind, _, valid := hitSourceID(source)
			if !valid || kind != 'p' || action != "block" || !selected[source] {
				continue
			}
			if user <= 0 || panel <= 0 || count < 0 {
				rows.Close()
				return domain.ErrUnavailable
			}
			b.user(user).sources[source] = auditReadAdd(b.user(user).sources[source], count)
			b.relate(user, panel)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *destRiskBuilder) readLosses(tx *gorm.DB, since, until time.Time) error {
	panels := make([]int64, 0, len(b.panelUsers))
	for panel := range b.panelUsers {
		panels = append(panels, panel)
	}
	slices.Sort(panels)
	for start := 0; start < len(panels); start += 512 {
		rows, err := tx.Model(&destAuditLossHourlyRow{}).Select("panel_id", "kind", "rows", "events", "unmatched").Where("panel_id IN ? AND kind = ? AND observed_hour_ms >= ? AND observed_hour_ms < ?", panels[start:min(start+512, len(panels))], "block", since.UnixMilli(), until.UnixMilli()).Rows()
		if err != nil {
			return err
		}
		for rows.Next() {
			var panel, lost, events, unmatched int64
			var kind string
			if err := rows.Scan(&panel, &kind, &lost, &events, &unmatched); err != nil {
				rows.Close()
				return err
			}
			if kind != "block" {
				continue
			}
			if lost < 0 || events < 0 || unmatched < 0 {
				rows.Close()
				return domain.ErrUnavailable
			}
			for user := range b.panelUsers[panel] {
				v := b.user(user)
				v.losses.Rows = auditReadAdd(v.losses.Rows, lost)
				v.losses.Events = auditReadAdd(v.losses.Events, events)
				v.losses.Unmatched = auditReadAdd(v.losses.Unmatched, unmatched)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

var _ ports.DestRiskReadRepo = (*DestAuditRepo)(nil)
