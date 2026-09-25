package sqlstore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"

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
	// Tier is the tier the last over-sample was judged at (domain.GeoTier:
	// "", country, region, city), kept while the flag is latched. Persisted
	// with the counters so a restart keeps WHY as well as whether: a latched
	// flag whose tier came back empty could no longer say what raised it.
	Tier string `gorm:"size:16;not null;default:''"`
	// BanOver is consecutive judged polls over the SUSPENSION tolerances —
	// its own counter, because the suspension line is looser than the flag
	// line and a sample between the two keeps one streak and breaks the
	// other. Persisted for the same reason as Over: a restart must neither
	// extend nor shorten the sustain window, and one that reset it would
	// restart every window on every deploy.
	BanOver int `gorm:"not null;default:0"`

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
	// Places is the comma-joined list of COUNTRIES behind the verdict. A
	// short, bounded list, so a scalar column beats a join table nothing else
	// references. A row an older build wrote may still hold its scope's
	// places ("CC/City") until the user's next judgement. Save strips commas
	// from each place, since one would split it in two on the way back.
	Places string `gorm:"size:512;not null;default:''"`
	// LiveIPs is how many distinct addresses the upstream still remembered
	// for this user — its whole retention window, not the number judged
	// (that is Concurrent).
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
	// Concurrent and Excluded split the live sample: sources judged (live at
	// poll time and not set aside) and sources set aside (shared exits, the
	// ignore list, infrastructure, internal ranges). Next to LiveIPs they show
	// how much of the window was only memory and how much was deliberately
	// not counted — without them a verdict drawn from 2 of 30 addresses looks
	// like one drawn from all 30.
	Concurrent int `gorm:"not null;default:0"`
	Excluded   int `gorm:"not null;default:0"`
	// Evidence is the structured account of the verdict (domain.GeoEvidence:
	// spots, exclusions, coverage, spread — never an address), as JSON.
	//
	// text, NULLABLE, and NO DEFAULT. MySQL refuses a DEFAULT on a TEXT
	// column (error 1101; TestSchemaNoDefaultOnTextColumns), and NULL is the
	// honest value for a row an older build wrote: "not recorded", which a
	// reader must be able to tell from "nothing found". The struct-typed JSON
	// shape of jsonLayout, except that the zero value is written as NULL.
	Evidence jsonGeoEvidence `gorm:"column:evidence"`

	// UpdatedAt lets an operator see how fresh a latched flag is, and lets a
	// later cleanup drop rows for users the poll has stopped seeing.
	UpdatedAt int64 `gorm:"autoUpdateTime:milli"`
}

// jsonGeoEvidence stores domain.GeoEvidence as a JSON text column.
//
// The zero value (V 0) is written as NULL rather than as an object saying
// v 0, so "no evidence recorded" has ONE stored form whether an older build
// wrote the row or a verdict was saved without evidence. Anything with V set
// is a real value and is written whole.
type jsonGeoEvidence domain.GeoEvidence

func (j jsonGeoEvidence) Value() (driver.Value, error) {
	if j.V == 0 {
		return nil, nil
	}
	b, err := json.Marshal(domain.GeoEvidence(j))
	return string(b), err
}

// GormDBDataType — see jsonTagFilter.GormDBDataType. A struct would otherwise
// let the Postgres driver infer a column type from its fields; pin it to text.
func (jsonGeoEvidence) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

// Scan reads NULL and the empty string as the zero value — a legacy row, not
// an error. A scan error fails the whole Find, so one such row would blank
// the admin view for every user. (Through GORM a NULL never reaches here: it
// scans into a **jsonGeoEvidence and database/sql just leaves that pointer
// nil. The nil case is the Scanner contract for any other reader; the empty
// string does reach here.) The destination is reset first so a reused value
// can never keep spots from a previous row.
func (j *jsonGeoEvidence) Scan(value any) error {
	*j = jsonGeoEvidence{}
	var b []byte
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for jsonGeoEvidence: %T", value)
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, (*domain.GeoEvidence)(j))
}

func (geoStreakRow) TableName() string { return "geo_streaks" }

// GeoStreakRepo is the traffic poll's streak store.
type GeoStreakRepo struct{ db *gorm.DB }

func NewGeoStreakRepo(db *gorm.DB) *GeoStreakRepo { return &GeoStreakRepo{db: db} }

// Load returns every stored streak, WITHOUT the evidence.
//
// Whole-table rather than per-user: the poll judges every user each cycle, so
// one read replaces one round trip per user, and the row count is bounded by
// the user count.
//
// Evidence is left out because this is the poll's read and the poll never
// uses it, while it is by far the widest column (up to GeoEvidenceMaxSpots
// named places per row) — read here, it would be paid for on every row,
// every cycle, only to be thrown away. Records from Load therefore carry a
// zero Evidence; List is the read that has it. Everything else is read,
// UpdatedAt included: when a user was last judged is part of the streak's
// state, not of its explanation.
func (r *GeoStreakRepo) Load(ctx context.Context) (map[int64]domain.GeoRecord, error) {
	var rows []geoStreakRow
	if err := r.db.WithContext(ctx).Omit("evidence").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]domain.GeoRecord, len(rows))
	for _, row := range rows {
		out[row.UserID] = row.toDomain()
	}
	return out, nil
}

// List returns every stored record, newest judgement first, for the admin
// view. Same rows as Load plus the evidence; a separate method because the
// poll wants a map it can look a user up in and a reader wants an ordered
// list with the explanation attached.
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
	streak := domain.GeoStreak{
		Over:    row.Over,
		Under:   row.Under,
		Flagged: row.Flag,
		Tier:    domain.GeoTier(row.Tier),
		BanOver: row.BanOver,
	}
	return domain.GeoRecord{
		UserID:      row.UserID,
		Streak:      streak,
		State:       domain.GeoState(row.State),
		Reason:      row.Reason,
		Places:      places,
		LiveIPs:     row.LiveIPs,
		Concurrent:  row.Concurrent,
		Excluded:    row.Excluded,
		Evidence:    domain.GeoEvidence(row.Evidence),
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
// Rows for users absent from this cycle are left alone rather than deleted.
// An idle user is NOT absent: every user holding a client is judged each
// cycle, and an idle sample freezes the streak and re-saves the row (which is
// also what keeps a latched flag's updated_at current). Absent means the poll
// did not judge the user at all this cycle, and their row has to survive
// that: deleting it would let a watched account shed its streak simply by not
// being judged once.
//
// Every column the row carries is named in DoUpdates. An upsert rewrites only
// the columns it names, so one left out keeps its FIRST value forever — a
// flag's tier from last month, a suspension streak that never resets,
// evidence from a different cycle than the verdict beside it.
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
			Tier:       string(rec.Streak.Tier),
			BanOver:    rec.Streak.BanOver,
			State:      string(rec.State),
			Reason:     truncate(rec.Reason, 512),
			Places:     truncate(joinPlaces(rec.Places), 512),
			LiveIPs:    rec.LiveIPs,
			Incomplete: !rec.Complete,
			Concurrent: rec.Concurrent,
			Excluded:   rec.Excluded,
			Evidence:   jsonGeoEvidence(rec.Evidence),
		})
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"over", "under", "flagged", "tier", "ban_over",
			"state", "reason", "places", "live_ips", "incomplete",
			"concurrent", "excluded", "evidence",
			"updated_at",
		}),
	}).CreateInBatches(rows, 200).Error
}

// joinPlaces joins places for the comma-separated column, dropping any comma
// inside a place first. Places are country codes today, but this column
// cannot know what a later caller hands it, and a place such as
// "US/Washington, D.C." would come back as two — a phantom that anything
// reading the count would judge. Losing the comma is cosmetic; gaining a
// place is not.
func joinPlaces(places []string) string {
	cleaned := make([]string, len(places))
	for i, p := range places {
		cleaned[i] = strings.ReplaceAll(p, ",", "")
	}
	return strings.Join(cleaned, ",")
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
	// Cut on a rune boundary. The reason and the place list carry city names
	// and admin-typed text, so a byte-wise slice can end mid-sequence and
	// store an invalid string — which some drivers reject and every reader
	// renders as a replacement character right where the explanation was.
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
