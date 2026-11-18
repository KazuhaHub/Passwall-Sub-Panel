package sqlstore

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// sourceGuardCloudflareRow is the last good fetch of Cloudflare's ranges: one
// row, id 1, replaced whole.
type sourceGuardCloudflareRow struct {
	ID        int64       `gorm:"primaryKey;autoIncrement:false"`
	CIDRs     jsonStrings `gorm:"column:cidrs;type:text"`
	FetchedAt time.Time
}

func (sourceGuardCloudflareRow) TableName() string { return "source_guard_cloudflare" }

// sourceGuardHitRow is the latest report of one refused source: (set, listener,
// ip) is its identity, so a source that keeps being refused is one row.
type sourceGuardHitRow struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	PanelID        int64  `gorm:"not null;index"`
	SetKey         string `gorm:"size:64;not null;uniqueIndex:uk_source_guard_hit,priority:1;index"`
	ListenerNodeID int64  `gorm:"not null;uniqueIndex:uk_source_guard_hit,priority:2"`
	IP             string `gorm:"column:ip;size:45;not null;uniqueIndex:uk_source_guard_hit,priority:3"`
	HitCount       int64  `gorm:"not null"`
	LastSeen       time.Time
}

func (sourceGuardHitRow) TableName() string { return "source_guard_hits" }

type sourceGuardRepo struct{ db *gorm.DB }

func (r *sourceGuardRepo) LoadCloudflare(ctx context.Context) (domain.CloudflareRanges, bool, error) {
	var row sourceGuardCloudflareRow
	err := r.db.WithContext(ctx).First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.CloudflareRanges{}, false, nil
	}
	if err != nil {
		return domain.CloudflareRanges{}, false, err
	}
	return domain.CloudflareRanges{CIDRs: append([]string(nil), row.CIDRs...), FetchedAt: row.FetchedAt.UTC()}, true, nil
}

func (r *sourceGuardRepo) SaveCloudflare(ctx context.Context, ranges domain.CloudflareRanges) error {
	row := sourceGuardCloudflareRow{ID: 1, CIDRs: jsonStrings(ranges.CIDRs), FetchedAt: ranges.FetchedAt.UTC()}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}

func (r *sourceGuardRepo) RecordHits(ctx context.Context, panelID int64, hits []domain.SourceGuardHit, cutoff time.Time, limit int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, hit := range hits {
			row := sourceGuardHitRow{
				PanelID: panelID, SetKey: hit.Set, ListenerNodeID: hit.ListenerNodeID, IP: hit.IP,
				HitCount: hit.Count, LastSeen: hit.LastSeen.UTC(),
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "set_key"}, {Name: "listener_node_id"}, {Name: "ip"}},
				DoUpdates: clause.AssignmentColumns([]string{"panel_id", "hit_count", "last_seen"}),
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("panel_id = ? AND last_seen < ?", panelID, cutoff.UTC()).Delete(&sourceGuardHitRow{}).Error; err != nil {
			return err
		}
		if limit <= 0 {
			return nil
		}
		var keep []int64
		if err := tx.Model(&sourceGuardHitRow{}).Where("panel_id = ?", panelID).
			Order("last_seen DESC, id DESC").Limit(limit).Pluck("id", &keep).Error; err != nil {
			return err
		}
		if len(keep) < limit {
			return nil
		}
		return tx.Where("panel_id = ? AND id NOT IN ?", panelID, keep).Delete(&sourceGuardHitRow{}).Error
	})
}

func (r *sourceGuardRepo) ListHits(ctx context.Context, panelIDs []int64) ([]domain.SourceGuardHit, error) {
	if len(panelIDs) == 0 {
		return nil, nil
	}
	var rows []sourceGuardHitRow
	if err := r.db.WithContext(ctx).Where("panel_id IN ?", panelIDs).Order("last_seen DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.SourceGuardHit, len(rows))
	for i, row := range rows {
		out[i] = domain.SourceGuardHit{
			PanelID: row.PanelID, Set: row.SetKey, ListenerNodeID: row.ListenerNodeID,
			IP: row.IP, Count: row.HitCount, LastSeen: row.LastSeen.UTC(),
		}
	}
	return out, nil
}

func (r *sourceGuardRepo) ClearHits(ctx context.Context, set, ip string) (int64, error) {
	res := r.db.WithContext(ctx).Where("set_key = ? AND ip = ?", set, ip).Delete(&sourceGuardHitRow{})
	return res.RowsAffected, res.Error
}
