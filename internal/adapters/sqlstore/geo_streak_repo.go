package sqlstore

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// geoStreakRow persists the between-poll state that makes a concurrent-location
// verdict stable instead of jittery.
//
// It has to survive a restart. The whole design of the detector is that a flag
// LATCHES — over tolerance for N consecutive checks to raise it, and within
// tolerance for M to clear it — and state that lives only in memory turns a
// process restart into a free acquittal. An account that is being watched would
// be cleared by a deploy, which is both wrong and trivially exploitable by
// anyone who notices the pattern.
//
// One row per user, overwritten each cycle. Not history: the streak is a
// running total, and keeping every sample would grow without bound for a value
// nothing reads twice.
type geoStreakRow struct {
	UserID int64 `gorm:"primaryKey"`
	// Over and Under are the two consecutive-sample counters. Two rather than
	// one signed value because flagging and clearing have different
	// thresholds, and a single counter would make an oscillating account drift
	// instead of settling.
	Over  int  `gorm:"not null;default:0"`
	Under int  `gorm:"not null;default:0"`
	Flag  bool `gorm:"column:flagged;not null;default:false"`
	// UpdatedAt lets an operator see how fresh a latched flag is, and lets a
	// later cleanup drop rows for users the poll has stopped seeing.
	UpdatedAt int64 `gorm:"autoUpdateTime:milli"`
}

func (geoStreakRow) TableName() string { return "geo_streaks" }

// GeoStreakRepo is the traffic poll's streak store.
type GeoStreakRepo struct{ db *gorm.DB }

func NewGeoStreakRepo(db *gorm.DB) *GeoStreakRepo { return &GeoStreakRepo{db: db} }

// Load returns every stored streak.
//
// Whole-table rather than per-user: the poll judges every user each cycle, so
// one read replaces one round trip per user, and the row count is bounded by
// the user count.
func (r *GeoStreakRepo) Load(ctx context.Context) (map[int64]domain.GeoStreak, error) {
	var rows []geoStreakRow
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]domain.GeoStreak, len(rows))
	for _, row := range rows {
		out[row.UserID] = domain.GeoStreak{Over: row.Over, Under: row.Under, Flagged: row.Flag}
	}
	return out, nil
}

// Save upserts this cycle's streaks.
//
// Upsert rather than delete-then-insert: a truncate would leave the table
// empty for the duration of the write, and a crash inside that window would
// clear every latched flag in the fleet.
//
// Rows for users absent from this cycle are left alone rather than deleted. A
// user with no live connections is not evaluated at all — idling must not shed
// a streak, which is the easiest evasion there is — so their row has to
// survive a cycle that did not mention them.
func (r *GeoStreakRepo) Save(ctx context.Context, streaks map[int64]domain.GeoStreak) error {
	if len(streaks) == 0 {
		return nil
	}
	rows := make([]geoStreakRow, 0, len(streaks))
	for uid, s := range streaks {
		rows = append(rows, geoStreakRow{UserID: uid, Over: s.Over, Under: s.Under, Flag: s.Flagged})
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"over", "under", "flagged", "updated_at"}),
	}).CreateInBatches(rows, 200).Error
}
