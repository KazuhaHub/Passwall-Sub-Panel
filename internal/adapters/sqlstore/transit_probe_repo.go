package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// transitProbeRow is the last probe window an entry server reported for one
// probed object, with the hysteresis state that decides withholding. One row
// per (server, object key, kind); the key is a link (lnk_N: e2e and transport)
// or a forward (fwd_N: forward) of that server's own config. See
// domain.ProbeState.
type transitProbeRow struct {
	PanelID   int64  `gorm:"primaryKey;autoIncrement:false"`
	ObjectKey string `gorm:"primaryKey;size:32"`
	Kind      string `gorm:"primaryKey;size:16"`

	Attempts      int    `gorm:"not null;default:0"`
	Failures      int    `gorm:"not null;default:0"`
	RTTMinMS      int    `gorm:"column:rtt_min_ms;not null;default:0"`
	RTTMedianMS   int    `gorm:"column:rtt_median_ms;not null;default:0"`
	RTTMaxMS      int    `gorm:"column:rtt_max_ms;not null;default:0"`
	LastError     string `gorm:"size:32;not null;default:''"`
	LastSuccessMS int64  `gorm:"not null;default:0"`
	LoopbackPort  int    `gorm:"not null;default:0"`

	State       string    `gorm:"size:16;not null"`
	Consecutive int       `gorm:"not null;default:0"`
	StateSince  time.Time `gorm:"not null"`
	Withheld    bool      `gorm:"not null;default:false;index:idx_transit_probe_withheld"`

	ReportedAt      time.Time `gorm:"not null"`
	ValidUntil      time.Time `gorm:"not null"`
	AgentReportedMS int64     `gorm:"not null;default:0"`
	CountedAt       time.Time `gorm:"not null"`
}

func (transitProbeRow) TableName() string { return "transit_probe_state" }

func (r transitProbeRow) toDomain() domain.ProbeState {
	return domain.ProbeState{
		PanelID: r.PanelID, ObjectKey: r.ObjectKey, Kind: r.Kind,
		Attempts: r.Attempts, Failures: r.Failures,
		RTTMinMS: r.RTTMinMS, RTTMedianMS: r.RTTMedianMS, RTTMaxMS: r.RTTMaxMS,
		LastError: r.LastError, LastSuccessMS: r.LastSuccessMS, LoopbackPort: r.LoopbackPort,
		State: domain.ProbeHealth(r.State), Consecutive: r.Consecutive, StateSince: r.StateSince, Withheld: r.Withheld,
		ReportedAt: r.ReportedAt, ValidUntil: r.ValidUntil, AgentReportedMS: r.AgentReportedMS, CountedAt: r.CountedAt,
	}
}

func probeRowOf(s domain.ProbeState) transitProbeRow {
	if s.CountedAt.IsZero() {
		s.CountedAt = s.ReportedAt
	}
	return transitProbeRow{
		PanelID: s.PanelID, ObjectKey: s.ObjectKey, Kind: s.Kind,
		Attempts: s.Attempts, Failures: s.Failures,
		RTTMinMS: s.RTTMinMS, RTTMedianMS: s.RTTMedianMS, RTTMaxMS: s.RTTMaxMS,
		LastError: s.LastError, LastSuccessMS: s.LastSuccessMS, LoopbackPort: s.LoopbackPort,
		State: string(s.State), Consecutive: s.Consecutive, StateSince: s.StateSince.UTC(), Withheld: s.Withheld,
		ReportedAt: s.ReportedAt.UTC(), ValidUntil: s.ValidUntil.UTC(), AgentReportedMS: s.AgentReportedMS, CountedAt: s.CountedAt.UTC(),
	}
}

type transitProbeRepo struct{ db *gorm.DB }

// NewTransitProbeRepo returns the probe state store.
func NewTransitProbeRepo(db *gorm.DB) *transitProbeRepo { return &transitProbeRepo{db: db} }

// Apply reads the row of one object under a row lock, hands it (nil when there
// is none) to update, and stores what update returns. update may refuse with
// write=false (a replayed report), which stores nothing. It returns the row as
// it was and as it is now.
func (r *transitProbeRepo) Apply(ctx context.Context, panelID int64, key, kind string,
	update func(prev *domain.ProbeState) (next domain.ProbeState, write bool, err error)) (prev *domain.ProbeState, next domain.ProbeState, err error) {
	// Two attempts: the second only runs when another writer created the row
	// between this one's read and insert.
	for attempt := 0; attempt < 2; attempt++ {
		prev, next = nil, domain.ProbeState{}
		retry := false
		err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row transitProbeRow
			found := true
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("panel_id = ? AND object_key = ? AND kind = ?", panelID, key, kind).First(&row).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				found = false
			}
			var before *domain.ProbeState
			if found {
				d := row.toDomain()
				before = &d
			}
			after, write, err := update(before)
			if err != nil {
				return err
			}
			prev = before
			if !write {
				next = domain.ProbeState{}
				if before != nil {
					next = *before
				}
				return nil
			}
			after.PanelID, after.ObjectKey, after.Kind = panelID, key, kind
			next = after
			stored := probeRowOf(after)
			if found {
				return tx.Save(&stored).Error
			}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stored)
			if result.Error != nil {
				return result.Error
			}
			retry = result.RowsAffected == 0
			return nil
		})
		if err != nil {
			return nil, domain.ProbeState{}, err
		}
		if !retry {
			return prev, next, nil
		}
	}
	return nil, domain.ProbeState{}, fmt.Errorf("probe state of %s/%s/%d: concurrent writers", key, kind, panelID)
}

// List returns every row.
func (r *transitProbeRepo) List(ctx context.Context) ([]domain.ProbeState, error) {
	var rows []transitProbeRow
	if err := r.db.WithContext(ctx).Order("panel_id ASC, object_key ASC, kind ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.ProbeState, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// ListProbePorts returns fresh end-to-end observations that identify an internal listener.
func (r *transitProbeRepo) ListProbePorts(ctx context.Context, panelID int64, now time.Time) ([]domain.ProbeState, error) {
	var rows []transitProbeRow
	if err := r.db.WithContext(ctx).
		Where("panel_id = ? AND kind = ? AND loopback_port > 0 AND valid_until > ?", panelID, nodeprotocol.ProbeKindE2E, now.UTC()).
		Order("loopback_port ASC, object_key ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.ProbeState, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// PruneExcept deletes the rows of one server whose object is not in keep: a
// link or forward that is no longer part of its config has nothing to report.
func (r *transitProbeRepo) PruneExcept(ctx context.Context, panelID int64, keep []string) (int64, error) {
	db := r.db.WithContext(ctx).Where("panel_id = ?", panelID)
	if len(keep) > 0 {
		db = db.Where("object_key NOT IN ?", keep)
	}
	result := db.Delete(&transitProbeRow{})
	return result.RowsAffected, result.Error
}

// ClearWithheld clears every withheld flag, for when withholding or probing is
// turned off: a setting that is off must stop withholding at once, whatever the
// rows were holding. It reports how many rows changed.
func (r *transitProbeRepo) ClearWithheld(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Model(&transitProbeRow{}).Where("withheld = ?", true).Update("withheld", false)
	return result.RowsAffected, result.Error
}

// probeLineRef names one managed line: the landing node and the line's id.
type probeLineRef struct {
	node  int64
	relay string
}

// attachProbeState fills the loaded probe fields: RelayLine.Probe and
// ProbeWithheld of managed lines (the entry's forward), Node.EgressProbe and
// EgressWithheld of tunnel nodes (the default exit's link). A row that is stale
// or unknown never withholds. forwardIDs maps (node, relay) to the forward row
// id its key is minted from, for the nodes that have managed lines.
func attachProbeState(ctx context.Context, db *gorm.DB, nodes []*domain.Node, forwardIDs map[probeLineRef]int64) error {
	type linkPair struct{ entry, exit int64 }
	wantLinks := map[linkPair]bool{}
	wantForwards := false
	for _, node := range nodes {
		if node == nil {
			continue
		}
		node.EgressWithheld, node.EgressProbe = false, nil
		if node.EgressPanelID != 0 {
			wantLinks[linkPair{node.PanelID, node.EgressPanelID}] = true
		}
		for i := range node.Relays {
			node.Relays[i].ProbeWithheld, node.Relays[i].Probe = false, nil
			if node.Relays[i].Managed() {
				wantForwards = true
			}
		}
	}
	if len(wantLinks) == 0 && !wantForwards {
		return nil
	}
	// A row belongs to the server that ran the probe: the node's own server for
	// its tunnel link, a managed line's entry server for its forward. Nothing
	// else is looked up below, so nothing else is read.
	servers := make([]int64, 0, len(wantLinks))
	for pair := range wantLinks {
		servers = append(servers, pair.entry)
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, relay := range node.Relays {
			if relay.Managed() {
				servers = append(servers, relay.EntryPanelID)
			}
		}
	}
	var rows []transitProbeRow
	db = db.WithContext(ctx)
	for _, chunk := range idChunks(servers) {
		var part []transitProbeRow
		if err := db.Where("panel_id IN ?", chunk).Find(&part).Error; err != nil {
			return fmt.Errorf("load probe state: %w", err)
		}
		rows = append(rows, part...)
	}
	if len(rows) == 0 {
		return nil
	}
	now := time.Now()
	// A flag counts only while withholding is on. Ingest and the settings write
	// both keep it in step, but they can cross, so the setting is read here too.
	withholding, err := probeWithholdingOn(db)
	if err != nil {
		return err
	}
	if !withholding {
		for i := range rows {
			rows[i].Withheld = false
		}
	}
	type objectID struct {
		panel int64
		key   string
	}
	byObject := map[objectID][]domain.ProbeState{}
	for _, row := range rows {
		id := objectID{row.PanelID, row.ObjectKey}
		byObject[id] = append(byObject[id], row.toDomain())
	}
	if len(wantLinks) > 0 {
		pairs := make([][2]int64, 0, len(wantLinks))
		for pair := range wantLinks {
			pairs = append(pairs, [2]int64{pair.entry, pair.exit})
		}
		links, err := findLinksOfPairs(ctx, db, pairs, "id", "entry_panel_id", "exit_panel_id")
		if err != nil {
			return fmt.Errorf("load probe links: %w", err)
		}
		linkKeys := map[linkPair]string{}
		for _, link := range links {
			linkKeys[linkPair{link.EntryPanelID, link.ExitPanelID}] = fmt.Sprintf("lnk_%d", link.ID)
		}
		for _, node := range nodes {
			if node == nil || node.EgressPanelID == 0 {
				continue
			}
			key, ok := linkKeys[linkPair{node.PanelID, node.EgressPanelID}]
			if !ok {
				continue
			}
			if states := byObject[objectID{node.PanelID, key}]; len(states) > 0 {
				quality := domain.QualityOf(states, now)
				node.EgressProbe, node.EgressWithheld = &quality, quality.Withheld
			}
		}
	}
	if wantForwards {
		for _, node := range nodes {
			if node == nil {
				continue
			}
			for i := range node.Relays {
				relay := &node.Relays[i]
				if !relay.Managed() {
					continue
				}
				id, ok := forwardIDs[probeLineRef{node.ID, relay.ID}]
				if !ok {
					continue
				}
				if states := byObject[objectID{relay.EntryPanelID, fmt.Sprintf("fwd_%d", id)}]; len(states) > 0 {
					quality := domain.QualityOf(states, now)
					relay.Probe, relay.ProbeWithheld = &quality, quality.Withheld
				}
			}
		}
	}
	return nil
}

// probeWithholdingOn reads whether probing and withholding are both on. A
// setting never written is the shipped default: probing on, withholding off.
func probeWithholdingOn(db *gorm.DB) (bool, error) {
	var settings []settingRow
	if err := db.Where("type = ? AND name IN ?", "transit_probe", []string{"off", "withhold"}).Find(&settings).Error; err != nil {
		return false, fmt.Errorf("load probe settings: %w", err)
	}
	on := map[string]bool{}
	for _, row := range settings {
		on[row.Name] = row.Value == "1" || row.Value == "true"
	}
	return on["withhold"] && !on["off"], nil
}
