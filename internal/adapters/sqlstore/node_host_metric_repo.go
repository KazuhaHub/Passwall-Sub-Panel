package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeHostMetricRepo struct{ db *gorm.DB }

var _ ports.NodeHostMetricRepo = (*nodeHostMetricRepo)(nil)

// counterReader converts the signed columns back to the unsigned values the wire
// contract carries.
//
// IT ACCUMULATES ONE ERROR INSTEAD OF RETURNING ONE PER FIELD, because there are
// around fifty counters and threading an error through each conversion would bury
// the shape of the row in error handling. The check itself cannot be dropped: the
// wire validated every counter against MaxInt64 on the way in, so a negative
// value here is corruption rather than a large number, and reinterpreting it as
// 2^64-x would turn a damaged row into a plausible reading.
type counterReader struct{ err error }

func (c *counterReader) take(value *int64, field string) *uint64 {
	if value == nil {
		return nil
	}
	if *value < 0 {
		if c.err == nil {
			c.err = fmt.Errorf("node host metric column %s is negative, which means the row is corrupt", field)
		}
		return nil
	}
	converted := uint64(*value)
	return &converted
}

func (r *nodeHostMetricRepo) Persist(ctx context.Context, request domain.NodeHostPersistRequest) (domain.NodeHostPersistResult, error) {
	if request.Observation == nil {
		return domain.NodeHostPersistResult{}, errors.New("node host persist requires an observation")
	}
	var result domain.NodeHostPersistResult
	// ONE TRANSACTION for latest, history and interfaces. A latest row that
	// committed while the history it came from rolled back would leave the detail
	// view describing a sample the charts never saw.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		latest, err := persistLatestObservation(tx, request.Observation)
		if err != nil {
			return err
		}
		result = latest
		if request.Sample == nil {
			return nil
		}
		inserted, err := insertMetricSample(tx, request.Sample, request.Interfaces)
		if err != nil {
			return err
		}
		result.HistoryInserted = inserted
		if !inserted {
			// The sample is already stored. That is a success, and it is what a
			// retried report looks like.
			result.Duplicate = true
		}
		return nil
	})
	if err != nil {
		return domain.NodeHostPersistResult{}, err
	}
	return result, nil
}

// persistLatestObservation applies the snapshot's three precedence rules in
// order, because each one answers a different question and getting the order
// wrong changes which of them wins.
func persistLatestObservation(tx *gorm.DB, observation *domain.NodeHostObservation) (domain.NodeHostPersistResult, error) {
	var existing nodeHostObservationRow
	err := tx.Where("agent_id = ?", observation.AgentID).Take(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row := observationRowFromDomain(observation)
		if createErr := tx.Create(&row).Error; createErr != nil {
			return domain.NodeHostPersistResult{}, fmt.Errorf("insert node host observation: %w", createErr)
		}
		return domain.NodeHostPersistResult{LatestUpdated: true}, nil
	}
	if err != nil {
		return domain.NodeHostPersistResult{}, fmt.Errorf("read node host observation: %w", err)
	}
	return applyLatestPrecedence(tx, &existing, observation)
}

func applyLatestPrecedence(tx *gorm.DB, existing *nodeHostObservationRow, observation *domain.NodeHostObservation) (domain.NodeHostPersistResult, error) {
	// 1. The agent retried a report the panel already has. Idempotent, and the
	//    FIRST receipt time is kept: it is when the observation actually arrived.
	if existing.SampleID == observation.SampleID && existing.PayloadDigest == observation.PayloadDigest {
		return domain.NodeHostPersistResult{Duplicate: true}, nil
	}
	// 2. One sample id, two different payloads. Neither can be trusted to be
	//    "the" sample, so the stored one stands, the new subtree is dropped, and
	//    the core sync still succeeds — a telemetry anomaly must not be able to
	//    fail a node's roster.
	if existing.SampleID == observation.SampleID {
		return domain.NodeHostPersistResult{IdentityConflict: true}, nil
	}
	// 3. A late request must not overwrite a newer snapshot. The panel is the one
	//    that knows receipt order, and rewinding would make the detail view
	//    describe a past state as the current one.
	if !observation.ReceivedAt.After(existing.ReceivedAt) {
		return domain.NodeHostPersistResult{}, nil
	}
	// A map rather than a struct so the zero values are written rather than
	// skipped: an empty boot id or an empty scope is a real value here.
	updates := map[string]any{
		"sample_id":      observation.SampleID,
		"payload_digest": observation.PayloadDigest,
		"collected_at":   observation.CollectedAt.UTC(),
		"received_at":    observation.ReceivedAt.UTC(),
		"boot_id":        observation.BootID,
		"resource_scope": observation.ResourceScope,
		"snapshot_json":  string(observation.SnapshotJSON),
		"updated_at":     time.Now().UTC(),
	}
	if err := tx.Model(&nodeHostObservationRow{}).
		Where("agent_id = ?", observation.AgentID).Updates(updates).Error; err != nil {
		return domain.NodeHostPersistResult{}, fmt.Errorf("update node host observation: %w", err)
	}
	// CreatedAt is deliberately not in the map: it records when this agent's
	// snapshot FIRST appeared, and an update is not a creation.
	return domain.NodeHostPersistResult{LatestUpdated: true}, nil
}

func insertMetricSample(tx *gorm.DB, sample *domain.NodeHostMetricSample, interfaces []domain.NodeInterfaceMetricSample) (bool, error) {
	row, err := sampleRowFromDomain(sample)
	if err != nil {
		return false, err
	}
	// DoNothing on (agent_id, sample_id): a retried report carries the sample the
	// panel already stored, and that key is what makes the retry recognisable
	// rather than a second row.
	insert := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "agent_id"}, {Name: "sample_id"}},
		DoNothing: true,
	}).Create(row)
	if insert.Error != nil {
		return false, fmt.Errorf("insert node host metric sample: %w", insert.Error)
	}
	if insert.RowsAffected == 0 {
		return false, nil
	}
	if len(interfaces) == 0 {
		return true, nil
	}
	rows := make([]nodeInterfaceMetricSampleRow, 0, len(interfaces))
	for index := range interfaces {
		rows = append(rows, interfaceRowFromDomain(&interfaces[index]))
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
		return false, fmt.Errorf("insert node interface metric samples: %w", err)
	}
	return true, nil
}

func (r *nodeHostMetricRepo) Latest(ctx context.Context, agentID string) (*domain.NodeHostObservation, error) {
	var row nodeHostObservationRow
	err := r.db.WithContext(ctx).Where("agent_id = ?", agentID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read node host observation: %w", err)
	}
	return row.toDomain(), nil
}

// LatestBatchByPanelIDs reads the list view's summaries in one query.
//
// The join is on the agent table because the list is keyed by panel id and only
// the agent row knows which panel an agent belongs to. A per-row query would be
// the N+1 the spec forbids, on the page an operator loads most often.
func (r *nodeHostMetricRepo) LatestBatchByPanelIDs(ctx context.Context, panelIDs []int64) (map[int64]domain.NodeHostSummary, error) {
	summaries := map[int64]domain.NodeHostSummary{}
	if len(panelIDs) == 0 {
		return summaries, nil
	}
	var rows []struct {
		PanelID       int64
		AgentID       string
		SampleID      string
		ReceivedAt    time.Time
		ResourceScope string
		SnapshotJSON  string
	}
	err := r.db.WithContext(ctx).
		Table("node_host_observations AS o").
		Select("a.panel_id AS panel_id, o.agent_id AS agent_id, o.sample_id AS sample_id, "+
			"o.received_at AS received_at, o.resource_scope AS resource_scope, o.snapshot_json AS snapshot_json").
		Joins("JOIN node_agents AS a ON a.agent_id = o.agent_id").
		Where("a.panel_id IN ?", panelIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read node host summaries: %w", err)
	}
	for _, row := range rows {
		summaries[row.PanelID] = domain.NodeHostSummary{
			PanelID: row.PanelID, AgentID: row.AgentID, SampleID: row.SampleID,
			ReceivedAt:    row.ReceivedAt.UTC(),
			ResourceScope: row.ResourceScope,
			Unavailable:   countUnavailable(row.SnapshotJSON),
		}
	}
	return summaries, nil
}

// countUnavailable reads the token count out of a stored snapshot.
//
// It scans rather than parsing the whole observation: the list needs a count, and
// decoding every snapshot on the page to produce one integer is work the panel
// would do on its hottest list endpoint.
func countUnavailable(snapshotJSON string) int {
	const key = `"unavailable":[`
	index := strings.Index(snapshotJSON, key)
	if index < 0 {
		return 0
	}
	rest := snapshotJSON[index+len(key):]
	end := strings.Index(rest, "]")
	if end < 0 {
		return 0
	}
	if end == 0 {
		return 0
	}
	count := 1
	for _, character := range rest[:end] {
		if character == ',' {
			count++
		}
	}
	return count
}

func (r *nodeHostMetricRepo) RawRange(ctx context.Context, agentID string, from, to time.Time, includePredecessor bool) ([]domain.NodeHostMetricSample, error) {
	samples, err := r.rawRange(ctx, agentID, from, to)
	if err != nil {
		return nil, err
	}
	if !includePredecessor {
		return samples, nil
	}
	// THE BASELINE, NOT A DATA POINT. A rate needs two samples, so the first point
	// of every window would have nothing to subtract without the row before it.
	// The caller is told this row is a baseline by its position — it is returned
	// first and its received_at precedes the window — and must not plot it.
	var predecessor nodeHostMetricSampleRow
	err = r.db.WithContext(ctx).
		Where("agent_id = ? AND received_at < ?", agentID, from.UTC()).
		Order("received_at DESC, id DESC").Take(&predecessor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return samples, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read node host metric predecessor: %w", err)
	}
	baseline, err := predecessor.toDomain()
	if err != nil {
		return nil, err
	}
	return append([]domain.NodeHostMetricSample{*baseline}, samples...), nil
}

func (r *nodeHostMetricRepo) rawRange(ctx context.Context, agentID string, from, to time.Time) ([]domain.NodeHostMetricSample, error) {
	var rows []nodeHostMetricSampleRow
	// Ordered by received_at with id as the tiebreak, so two samples that arrived
	// in the same millisecond still come back in a stable order — the rollup
	// differences adjacent rows, and an unstable order would make it
	// non-deterministic at exactly the boundary it cares about.
	err := r.db.WithContext(ctx).
		Where("agent_id = ? AND received_at >= ? AND received_at <= ?", agentID, from.UTC(), to.UTC()).
		Order("received_at, id").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read node host metric samples: %w", err)
	}
	samples := make([]domain.NodeHostMetricSample, 0, len(rows))
	for index := range rows {
		sample, err := rows[index].toDomain()
		if err != nil {
			return nil, err
		}
		samples = append(samples, *sample)
	}
	return samples, nil
}

func (r *nodeHostMetricRepo) InterfaceRange(ctx context.Context, agentID, interfaceName string, from, to time.Time) ([]domain.NodeInterfaceMetricSample, error) {
	var rows []nodeInterfaceMetricSampleRow
	err := r.db.WithContext(ctx).
		Where("agent_id = ? AND interface_name = ? AND received_at >= ? AND received_at <= ?",
			agentID, interfaceName, from.UTC(), to.UTC()).
		Order("received_at, interface_index").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read node interface metric samples: %w", err)
	}
	samples := make([]domain.NodeInterfaceMetricSample, 0, len(rows))
	for index := range rows {
		samples = append(samples, rows[index].toDomain())
	}
	return samples, nil
}

func (r *nodeHostMetricRepo) HourlyRange(ctx context.Context, agentID string, from, to time.Time) ([]domain.NodeHostMetricHourly, error) {
	var rows []nodeHostMetricHourlyRow
	err := r.db.WithContext(ctx).
		Where("agent_id = ? AND bucket_start >= ? AND bucket_start <= ?", agentID, from.UTC(), to.UTC()).
		Order("bucket_start").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read node host hourly metrics: %w", err)
	}
	hourly := make([]domain.NodeHostMetricHourly, 0, len(rows))
	for index := range rows {
		hourly = append(hourly, rows[index].toDomain())
	}
	return hourly, nil
}

// UpsertHourly writes a batch of hourly aggregates.
//
// The conflict target is (agent_id, bucket_start), which is what makes the rollup
// idempotent: recomputing a bucket after a crash, or re-running it before a
// prune, overwrites the same row rather than appending a second one.
func (r *nodeHostMetricRepo) UpsertHourly(ctx context.Context, rows []domain.NodeHostMetricHourly) error {
	if len(rows) == 0 {
		return nil
	}
	converted := make([]nodeHostMetricHourlyRow, 0, len(rows))
	for index := range rows {
		converted = append(converted, hourlyRowFromDomain(&rows[index]))
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "agent_id"}, {Name: "bucket_start"}},
		// Every column is replaced rather than merged: the aggregate is a pure
		// function of the raw rows in its bucket, so a partial update would mix
		// two computations of the same hour.
		UpdateAll: true,
	}).Create(&converted).Error
	if err != nil {
		return fmt.Errorf("upsert node host hourly metrics: %w", err)
	}
	return nil
}

// DeleteByAgentID removes every row belonging to one agent.
//
// It is transactional because nothing else would clean up: the interface rows are
// keyed by (agent, sample) and the samples by (agent, sample), so an interrupted
// delete would leave interface rows whose sample no longer exists — visible as
// gaps in an interface chart with nothing to explain them.
func (r *nodeHostMetricRepo) DeleteByAgentID(ctx context.Context, agentID string) error {
	if agentID == "" {
		return errors.New("agent id is required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("agent_id = ?", agentID).Delete(&nodeInterfaceMetricSampleRow{}).Error; err != nil {
			return fmt.Errorf("delete node interface metric samples: %w", err)
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&nodeHostMetricSampleRow{}).Error; err != nil {
			return fmt.Errorf("delete node host metric samples: %w", err)
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&nodeHostMetricHourlyRow{}).Error; err != nil {
			return fmt.Errorf("delete node host hourly metrics: %w", err)
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&nodeHostObservationRow{}).Error; err != nil {
			return fmt.Errorf("delete node host observation: %w", err)
		}
		return nil
	})
}

// Prune removes history older than the retention cutoffs, in bounded batches.
//
// IT DELETES IN BOUNDED BATCHES rather than one statement. A large installation's
// retention would otherwise hold a single long write transaction, and SQLite
// serialises writes — so the panel's own traffic poll would block behind its
// cleanup.
func (r *nodeHostMetricRepo) Prune(ctx context.Context, request domain.NodeHostPruneRequest) (domain.NodeHostPruneResult, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = defaultNodeHostPruneBatch
	}
	var result domain.NodeHostPruneResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Interfaces go first: they are keyed by the sample they belong to, so
		// removing the samples first would leave orphan interface rows that no
		// later delete could find.
		interfaceDeleted, err := deleteBatched(tx, &nodeInterfaceMetricSampleRow{}, "received_at < ?", request.RawBefore.UTC(), limit)
		if err != nil {
			return err
		}
		result.InterfaceDeleted = interfaceDeleted
		rawDeleted, err := deleteBatched(tx, &nodeHostMetricSampleRow{}, "received_at < ?", request.RawBefore.UTC(), limit)
		if err != nil {
			return err
		}
		result.RawDeleted = rawDeleted
		hourlyDeleted, err := deleteBatched(tx, &nodeHostMetricHourlyRow{}, "bucket_start < ?", request.HourlyBefore.UTC(), limit)
		if err != nil {
			return err
		}
		result.HourlyDeleted = hourlyDeleted
		return nil
	})
	if err != nil {
		return domain.NodeHostPruneResult{}, err
	}
	return result, nil
}

// defaultNodeHostPruneBatch bounds one cleanup pass. It is a count rather than a
// time window because the cost of a delete is per row, not per unit of history.
const defaultNodeHostPruneBatch = 5000

// deleteBatched removes at most limit rows matching a predicate.
//
// The subquery selects ids first, because the three dialects disagree about
// whether DELETE ... LIMIT exists and none of them agree on its spelling.
func deleteBatched(tx *gorm.DB, model any, where string, argument any, limit int) (int64, error) {
	var ids []int64
	selectErr := tx.Model(model).
		Where(where, argument).
		Order("id").Limit(limit).
		Pluck("id", &ids).Error
	if selectErr != nil {
		return 0, fmt.Errorf("select prunable rows: %w", selectErr)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := tx.Where("id IN ?", ids).Delete(model)
	if deleted.Error != nil {
		return 0, fmt.Errorf("prune rows: %w", deleted.Error)
	}
	return deleted.RowsAffected, nil
}
