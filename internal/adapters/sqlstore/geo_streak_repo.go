package sqlstore

import (
	"context"
	"strings"

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

	// The last verdict, stored alongside the counters that produced it.
	//
	// The counters alone answer "is this account flagged"; an operator
	// deciding whether to act needs WHY. A flag with no reason is not a basis
	// for touching somebody's account, and reconstructing one would mean
	// re-running the whole poll.
	//
	// Same row and same write as the streak, so the reason can never be from
	// a different cycle than the counters — two tables would let them drift
	// and an operator would have no way to tell which was current.
	State  string `gorm:"size:16;not null;default:''"`
	Reason string `gorm:"size:512;not null;default:''"`
	// Places is the comma-joined place list behind the verdict. A short,
	// bounded list (country codes, or "CC/City"), so a scalar column beats a
	// join table nothing else references.
	Places string `gorm:"size:512;not null;default:''"`
	// LiveIPs is how many distinct addresses backed the verdict.
	//
	// Incomplete says that number is only a FLOOR, because a panel holding
	// this user's clients could not be read. Showing a floor as a total reads
	// as "this account is fine" exactly when the evidence is missing.
	//
	// Stored inverted — "incomplete" rather than "complete" — because GORM
	// treats a bool's zero value as unset and applies the column default. A
	// `complete bool default:true` column therefore CANNOT store false: every
	// partial count would come back claiming to be a total, which is the exact
	// failure this field exists to prevent. Phrasing it so the alarming state
	// is the non-zero one removes the trap instead of working around it.
	LiveIPs    int  `gorm:"not null;default:0"`
	Incomplete bool `gorm:"not null;default:false"`

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
func (r *GeoStreakRepo) Load(ctx context.Context) (map[int64]domain.GeoRecord, error) {
	var rows []geoStreakRow
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]domain.GeoRecord, len(rows))
	for _, row := range rows {
		out[row.UserID] = row.toDomain()
	}
	return out, nil
}

// List returns every stored record, newest judgement first, for the admin
// view. Same rows as Load; a separate method because the poll wants a map it
// can look a user up in and a reader wants an ordered list.
func (r *GeoStreakRepo) List(ctx context.Context) ([]domain.GeoRecord, error) {
	var rows []geoStreakRow
	if err := r.db.WithContext(ctx).Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.GeoRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

func (row geoStreakRow) toDomain() domain.GeoRecord {
	var places []string
	if row.Places != "" {
		places = strings.Split(row.Places, ",")
	}
	return domain.GeoRecord{
		UserID:      row.UserID,
		Streak:      domain.GeoStreak{Over: row.Over, Under: row.Under, Flagged: row.Flag},
		State:       domain.GeoState(row.State),
		Reason:      row.Reason,
		Places:      places,
		LiveIPs:     row.LiveIPs,
		Complete:    !row.Incomplete,
		UpdatedAtMS: row.UpdatedAt,
	}
}

// Save upserts this cycle's records.
//
// Upsert rather than delete-then-insert: a truncate would leave the table
// empty for the duration of the write, and a crash inside that window would
// clear every latched flag in the fleet.
//
// Rows for users absent from this cycle are left alone rather than deleted. A
// user with no live connections is not evaluated at all — idling must not shed
// a streak, which is the easiest evasion there is — so their row has to
// survive a cycle that did not mention them.
func (r *GeoStreakRepo) Save(ctx context.Context, records map[int64]domain.GeoRecord) error {
	if len(records) == 0 {
		return nil
	}
	rows := make([]geoStreakRow, 0, len(records))
	for uid, rec := range records {
		rows = append(rows, geoStreakRow{
			UserID:     uid,
			Over:       rec.Streak.Over,
			Under:      rec.Streak.Under,
			Flag:       rec.Streak.Flagged,
			State:      string(rec.State),
			Reason:     truncate(rec.Reason, 512),
			Places:     truncate(strings.Join(rec.Places, ","), 512),
			LiveIPs:    rec.LiveIPs,
			Incomplete: !rec.Complete,
		})
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"over", "under", "flagged",
			"state", "reason", "places", "live_ips", "incomplete",
			"updated_at",
		}),
	}).CreateInBatches(rows, 200).Error
}

// truncate keeps a generated string inside its column.
//
// The reason and place list are assembled from a policy an admin controls (a
// co-travel set can name many places), so neither is bounded by anything this
// package owns. Truncating is better than failing the write: losing the tail of
// an explanation costs readability, and failing the write costs the whole
// fleet's hysteresis for that cycle.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
