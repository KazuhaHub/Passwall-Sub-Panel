package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeAgentTaskRow struct {
	TaskID  string `gorm:"primaryKey;size:128;index:idx_node_agent_task_offer,priority:4"`
	AgentID string `gorm:"size:64;not null;index:idx_node_agent_task_offer,priority:1;uniqueIndex:uk_node_agent_task_idempotency,priority:1"`
	Kind    string `gorm:"size:96;not null"`
	Args    []byte `gorm:"not null"`
	// InputSHA256 binds both Kind and Args using the shared wire helper.
	InputSHA256 string `gorm:"size:64;not null"`
	Status      string `gorm:"size:16;not null;default:'queued';index:idx_node_agent_task_offer,priority:2;check:chk_node_agent_task_status,status IN ('queued','offered','succeeded','failed','indeterminate')"`

	// A nil key means the caller deliberately opted out of request-level
	// idempotency. SQL unique indexes permit multiple NULL values on all three
	// supported databases.
	IdempotencyKeySHA256 *string `gorm:"size:64;uniqueIndex:uk_node_agent_task_idempotency,priority:2"`
	SupersedesTaskID     string  `gorm:"size:128;index"`
	// Nullable TEXT adds safely to populated databases without inventing
	// deadlines for old tasks. All non-NULL reads/writes pass the strict codec.
	Lifecycle            *nodeAgentTaskLifecycleJSON `gorm:"type:text"`
	DispatchClosedAt     *time.Time
	DispatchClosedReason string `gorm:"size:32;not null;default:''"`

	ResultOK            *bool
	ResultIndeterminate bool `gorm:"not null;default:false"`
	Result              []byte
	ResultErrorCode     string `gorm:"size:128"`
	ResultError         string `gorm:"type:text"`

	OfferCount     int `gorm:"not null;default:0"`
	FirstOfferedAt *time.Time
	LastOfferedAt  *time.Time
	CompletedAt    *time.Time `gorm:"index:idx_node_agent_task_completed"`
	CreatedAt      time.Time  `gorm:"index:idx_node_agent_task_offer,priority:3"`
	UpdatedAt      time.Time
}

func (nodeAgentTaskRow) TableName() string { return "node_agent_tasks" }

type nodeAgentTaskLifecycleJSON domain.NodeTaskLifecycleSnapshot

func (nodeAgentTaskLifecycleJSON) GormDataType() string                          { return "text" }
func (nodeAgentTaskLifecycleJSON) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

func (s nodeAgentTaskLifecycleJSON) Value() (driver.Value, error) {
	snapshot := domain.NodeTaskLifecycleSnapshot(s)
	if err := snapshot.Validate(); err != nil {
		return nil, errors.New("native task lifecycle snapshot failed storage validation")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, errors.New("native task lifecycle snapshot could not be encoded")
	}
	return string(payload), nil
}

func (s *nodeAgentTaskLifecycleJSON) Scan(value any) error {
	var payload []byte
	switch raw := value.(type) {
	case string:
		payload = []byte(raw)
	case []byte:
		payload = raw
	default:
		return errors.New("native task lifecycle snapshot has invalid storage type")
	}
	var snapshot domain.NodeTaskLifecycleSnapshot
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return errors.New("native task lifecycle snapshot could not be decoded")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("native task lifecycle snapshot contains trailing data")
	}
	if err := snapshot.Validate(); err != nil {
		return errors.New("native task lifecycle snapshot failed storage validation")
	}
	*s = nodeAgentTaskLifecycleJSON(snapshot)
	return nil
}

func nodeAgentTaskLifecycleFromDomain(snapshot *domain.NodeTaskLifecycleSnapshot) *nodeAgentTaskLifecycleJSON {
	if snapshot == nil {
		return nil
	}
	copy := nodeAgentTaskLifecycleJSON(*snapshot)
	return &copy
}

func (s *nodeAgentTaskLifecycleJSON) toDomain() *domain.NodeTaskLifecycleSnapshot {
	if s == nil {
		return nil
	}
	snapshot := domain.NodeTaskLifecycleSnapshot(*s)
	return snapshot.Clone()
}

func (r *nodeAgentTaskRow) toDomain() *domain.NodeAgentTask {
	if r == nil {
		return nil
	}
	return &domain.NodeAgentTask{
		TaskID: r.TaskID, AgentID: r.AgentID, Kind: r.Kind,
		Args: append([]byte(nil), r.Args...), InputSHA256: r.InputSHA256,
		Status:               domain.NodeAgentTaskStatus(r.Status),
		IdempotencyKeySHA256: cloneStringPointer(r.IdempotencyKeySHA256),
		SupersedesTaskID:     r.SupersedesTaskID,
		Lifecycle:            r.Lifecycle.toDomain(),
		DispatchClosedAt:     cloneTimePointer(r.DispatchClosedAt), DispatchClosedReason: r.DispatchClosedReason,
		ResultOK:            cloneBoolPointer(r.ResultOK),
		ResultIndeterminate: r.ResultIndeterminate,
		Result:              append([]byte(nil), r.Result...),
		ResultErrorCode:     r.ResultErrorCode, ResultError: r.ResultError,
		OfferCount: r.OfferCount, FirstOfferedAt: cloneTimePointer(r.FirstOfferedAt),
		LastOfferedAt: cloneTimePointer(r.LastOfferedAt), CompletedAt: cloneTimePointer(r.CompletedAt),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func nodeAgentTaskFromDomain(task *domain.NodeAgentTask) *nodeAgentTaskRow {
	return &nodeAgentTaskRow{
		TaskID: task.TaskID, AgentID: task.AgentID, Kind: task.Kind,
		Args: append([]byte{}, task.Args...), InputSHA256: task.InputSHA256,
		Status: string(task.Status), IdempotencyKeySHA256: cloneStringPointer(task.IdempotencyKeySHA256),
		SupersedesTaskID: task.SupersedesTaskID,
		Lifecycle:        nodeAgentTaskLifecycleFromDomain(task.Lifecycle),
		DispatchClosedAt: cloneTimePointer(task.DispatchClosedAt), DispatchClosedReason: task.DispatchClosedReason,
		ResultOK:            cloneBoolPointer(task.ResultOK),
		ResultIndeterminate: task.ResultIndeterminate,
		Result:              append([]byte(nil), task.Result...),
		ResultErrorCode:     task.ResultErrorCode, ResultError: task.ResultError,
		OfferCount: task.OfferCount, FirstOfferedAt: cloneTimePointer(task.FirstOfferedAt),
		LastOfferedAt: cloneTimePointer(task.LastOfferedAt), CompletedAt: cloneTimePointer(task.CompletedAt),
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

// The active backlog is intentionally a compiled safety bound, not a live UI
// setting. Dynamic lowering would need a separate over-limit state and would
// put a settings-table read into this transaction's owner-lock order. Four
// maximum-size offer windows allow useful offline buffering while bounding
// PSP's active backlog and unacknowledged node inputs. Terminal retention is
// a separate gate; this is not a bound on the complete task history.
const (
	defaultMaxActiveNodeAgentTasks    int64 = 256
	defaultMaxActiveNodeAgentArgBytes int64 = 16 << 20
)

type nodeAgentTaskQuota struct {
	MaxActiveTasks    int64
	MaxActiveArgBytes int64
}

func defaultNodeAgentTaskQuota() nodeAgentTaskQuota {
	return nodeAgentTaskQuota{
		MaxActiveTasks:    defaultMaxActiveNodeAgentTasks,
		MaxActiveArgBytes: defaultMaxActiveNodeAgentArgBytes,
	}
}

type nodeAgentTaskRepo struct {
	db              *gorm.DB
	quota           nodeAgentTaskQuota
	quarantineQuota nodeAgentTaskQuarantineQuota
}

func newNodeAgentTaskRepo(db *gorm.DB, quota nodeAgentTaskQuota) *nodeAgentTaskRepo {
	return &nodeAgentTaskRepo{db: db, quota: quota, quarantineQuota: defaultNodeAgentTaskQuarantineQuota()}
}

type nodeAgentTaskActiveUsage struct {
	Tasks    int64 `gorm:"column:active_tasks"`
	ArgBytes int64 `gorm:"column:active_arg_bytes"`
}

func (r *nodeAgentTaskRepo) CreateOrGet(ctx context.Context, task *domain.NodeAgentTask) (*domain.NodeAgentTask, bool, error) {
	if err := validateNewNodeAgentTask(task); err != nil {
		return nil, false, err
	}
	incoming := nodeAgentTaskFromDomain(task)
	var stored nodeAgentTaskRow
	created := false
	err := runTransactionWithRetry(ctx, r.db, func(tx *gorm.DB) error {
		// A prior attempt may have populated these before the database chose it
		// as a deadlock victim. Only state from the committed attempt may escape.
		stored = nodeAgentTaskRow{}
		created = false
		if _, err := lockNodeAgentByAgentID(tx, incoming.AgentID); err != nil {
			return err
		}
		if incoming.SupersedesTaskID != "" {
			var owner struct{ TaskID, AgentID string }
			if err := tx.Model(&nodeAgentTaskRow{}).Select("task_id, agent_id").
				Where("task_id = ?", incoming.SupersedesTaskID).First(&owner).Error; err != nil {
				return fmt.Errorf("create native agent task: superseded task: %w", wrapNotFound(err))
			}
			if owner.TaskID != incoming.SupersedesTaskID || owner.AgentID != incoming.AgentID {
				return fmt.Errorf("%w: superseded task must match the same agent identity exactly", domain.ErrConflict)
			}
			var prior nodeAgentTaskRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("agent_id = ? AND task_id = ?", incoming.AgentID, incoming.SupersedesTaskID).First(&prior).Error; err != nil {
				return fmt.Errorf("create native agent task: superseded task: %w", wrapNotFound(err))
			}
			// Default MySQL collations fold case. Task IDs are now canonical on
			// the wire, but this exact check also protects upgraded databases
			// containing rows written before that validator existed.
			if prior.TaskID != incoming.SupersedesTaskID {
				return fmt.Errorf("%w: superseded task ID must match stored identity exactly", domain.ErrConflict)
			}
			if prior.AgentID != incoming.AgentID || domain.NodeAgentTaskStatus(prior.Status) != domain.NodeAgentTaskFailed {
				return fmt.Errorf("%w: superseded task must be a failed task for the same agent", domain.ErrConflict)
			}
		}

		existing, matchedByTaskID, found, err := findNodeAgentTaskConflict(tx, incoming)
		if err != nil {
			return err
		}
		if found {
			if !sameTaskRequest(existing, incoming, matchedByTaskID) {
				return fmt.Errorf("%w: native agent task ID or idempotency key reused with different input", domain.ErrConflict)
			}
			stored = *existing
			return nil
		}
		// An orphan receipt fences only this agent's future use of this ID.
		// Existing task/idempotency replays above must remain available at capacity
		// and after dispatch closure. Foreign evidence never reserves global IDs.
		var fenced []nodeAgentTaskResultQuarantineRow
		if err := tx.Where("agent_id = ? AND task_id = ?", incoming.AgentID, incoming.TaskID).Limit(1).Find(&fenced).Error; err != nil {
			return err
		}
		if len(fenced) != 0 {
			return fmt.Errorf("%w: native agent task ID has quarantined result evidence", domain.ErrConflict)
		}
		if err := r.enforceActiveQuota(tx, incoming); err != nil {
			return err
		}

		// MySQL cannot render GORM's empty DoNothing action as valid SQL. Use a
		// dialect-aware no-op and never infer insertion from RowsAffected: MySQL's
		// CLIENT_FOUND_ROWS setting changes that value for self-assignments.
		if err := tx.Clauses(nodeAgentTaskInsertConflictClause(tx.Dialector.Name())).Create(incoming).Error; err != nil {
			return err
		}
		existing, matchedByTaskID, found, err = findNodeAgentTaskConflict(tx, incoming)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("create native agent task: inserted row could not be read back")
		}
		if !sameTaskRequest(existing, incoming, matchedByTaskID) {
			return fmt.Errorf("%w: native agent task ID or idempotency key reused concurrently with different input", domain.ErrConflict)
		}
		stored = *existing
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return stored.toDomain(), created, nil
}

// enforceActiveQuota runs after identity/idempotency replay resolution and
// immediately before INSERT. The node_agents owner row is already locked, and
// every task create/offer/complete plus native-agent deletion takes that same
// lock first. This makes the aggregate and insertion one serial admission
// decision per agent on SQLite, MySQL, and PostgreSQL without a denormalized
// counter that could drift.
func (r *nodeAgentTaskRepo) enforceActiveQuota(tx *gorm.DB, incoming *nodeAgentTaskRow) error {
	var usage nodeAgentTaskActiveUsage
	if err := tx.Model(&nodeAgentTaskRow{}).
		Select("COUNT(*) AS active_tasks, COALESCE(SUM(LENGTH(args)), 0) AS active_arg_bytes").
		Where("agent_id = ? AND status IN ?", incoming.AgentID,
			[]string{string(domain.NodeAgentTaskQueued), string(domain.NodeAgentTaskOffered)}).
		Scan(&usage).Error; err != nil {
		return fmt.Errorf("measure native agent active task quota: %w", err)
	}
	return checkNodeAgentTaskActiveQuota(r.quota, usage, int64(len(incoming.Args)))
}

func checkNodeAgentTaskActiveQuota(quota nodeAgentTaskQuota, usage nodeAgentTaskActiveUsage, requestedArgBytes int64) error {
	// Invalid internal configuration is fail-closed. In particular, zero never
	// acquires a surprising "unlimited" meaning for a resource safety cap.
	if quota.MaxActiveTasks <= 0 {
		return fmt.Errorf("%w: native agent active task count quota is not positive", domain.ErrResourceExhausted)
	}
	if quota.MaxActiveArgBytes <= 0 {
		return fmt.Errorf("%w: native agent active task byte quota is not positive", domain.ErrResourceExhausted)
	}
	if usage.Tasks < 0 || usage.ArgBytes < 0 || requestedArgBytes < 0 {
		return errors.New("native agent active task quota contains a negative value")
	}
	if usage.Tasks >= quota.MaxActiveTasks {
		return fmt.Errorf("%w: native agent active task count used=%d requested=1 limit=%d",
			domain.ErrResourceExhausted, usage.Tasks, quota.MaxActiveTasks)
	}
	// Subtract before comparing so a corrupted or future wider database value
	// cannot wrap an int64 addition and accidentally reopen admission.
	if usage.ArgBytes > quota.MaxActiveArgBytes || requestedArgBytes > quota.MaxActiveArgBytes-usage.ArgBytes {
		return fmt.Errorf("%w: native agent active task bytes used=%d requested=%d limit=%d",
			domain.ErrResourceExhausted, usage.ArgBytes, requestedArgBytes, quota.MaxActiveArgBytes)
	}
	return nil
}

func findNodeAgentTaskConflict(tx *gorm.DB, incoming *nodeAgentTaskRow) (*nodeAgentTaskRow, bool, bool, error) {
	var stored nodeAgentTaskRow
	err := tx.Where("task_id = ?", incoming.TaskID).First(&stored).Error
	if err == nil {
		return &stored, true, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, false, fmt.Errorf("create native agent task: resolve task ID: %w", err)
	}
	if incoming.IdempotencyKeySHA256 == nil {
		return nil, false, false, nil
	}
	err = tx.Where("agent_id = ? AND idempotency_key_sha256 = ?", incoming.AgentID, *incoming.IdempotencyKeySHA256).
		First(&stored).Error
	if err == nil {
		return &stored, false, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, false, nil
	}
	return nil, false, false, fmt.Errorf("create native agent task: resolve idempotency key: %w", err)
}

func nodeAgentTaskInsertConflictClause(dialect string) clause.OnConflict {
	if dialect == "mysql" {
		return clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{
			"task_id": clause.Column{Name: "task_id"},
		})}
	}
	return clause.OnConflict{DoNothing: true}
}

func (r *nodeAgentTaskRepo) GetByTaskID(ctx context.Context, taskID string) (*domain.NodeAgentTask, error) {
	if taskID == "" {
		return nil, fmt.Errorf("%w: task ID is required", domain.ErrValidation)
	}
	var row nodeAgentTaskRow
	if err := r.db.WithContext(ctx).Where("task_id = ?", taskID).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	if row.TaskID != taskID {
		return nil, fmt.Errorf("%w: native agent task", domain.ErrNotFound)
	}
	return row.toDomain(), nil
}

func (r *nodeAgentTaskRepo) Offer(ctx context.Context, agentID string, support ports.NodeAgentTaskOfferSupport, limit, maxTaskJSONBytes int, offeredAt time.Time) ([]*domain.NodeAgentTask, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent ID is required", domain.ErrValidation)
	}
	if offeredAt.IsZero() || offeredAt.UnixMilli() <= 0 {
		return nil, fmt.Errorf("%w: positive offered time is required", domain.ErrValidation)
	}
	kinds := make(map[string]bool, len(support.EligibleKinds))
	for _, kind := range support.EligibleKinds {
		kinds[kind] = kind != ""
	}
	if limit <= 0 || limit > nodeprotocol.MaxTasksPerResponse {
		limit = nodeprotocol.MaxTasksPerResponse
	}
	offeredAt = offeredAt.UTC()
	rows := make([]nodeAgentTaskRow, 0, limit)
	err := runTransactionWithRetry(ctx, r.db, func(tx *gorm.DB) error {
		rows = rows[:0]
		if _, err := lockNodeAgentByAgentID(tx, agentID); err != nil {
			return err
		}
		// This is the first consistent read after the owner lock, including on
		// MySQL REPEATABLE READ. All same-owner mutations share that lock. Resolve
		// public IDs before locking full rows so the optimizer cannot lock a
		// foreign owner's opaque input while filtering the global primary key.
		// Scan the complete bounded open backlog before kind/count/wire filters:
		// expired or unsupported tasks must not starve eligible rows behind them.
		statuses := []string{string(domain.NodeAgentTaskQueued), string(domain.NodeAgentTaskOffered)}
		var ownership []struct{ TaskID, AgentID string }
		if err := tx.Model(&nodeAgentTaskRow{}).Select("task_id, agent_id").
			Where("agent_id = ? AND status IN ? AND dispatch_closed_at IS NULL", agentID, statuses).
			Order("created_at ASC, task_id ASC").Limit(int(defaultMaxActiveNodeAgentTasks) + 1).Find(&ownership).Error; err != nil {
			return err
		}
		if len(ownership) > int(defaultMaxActiveNodeAgentTasks) {
			return errors.New("offer native agent tasks: stored open backlog exceeds safety bound")
		}
		ownIDs := make([]string, 0, len(ownership))
		knownIDs := make(map[string]bool, len(ownership))
		for _, owner := range ownership {
			if owner.AgentID != agentID {
				return fmt.Errorf("%w: stored task owner does not match exactly", domain.ErrConflict)
			}
			ownIDs = append(ownIDs, owner.TaskID)
			knownIDs[owner.TaskID] = true
		}
		if len(ownIDs) == 0 {
			return nil
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND task_id IN ? AND status IN ? AND dispatch_closed_at IS NULL", agentID, ownIDs, statuses).
			Order("created_at ASC, task_id ASC").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(ownIDs) {
			return fmt.Errorf("%w: stored task ownership changed concurrently", domain.ErrConflict)
		}
		var evidence []struct{ TaskID, AgentID string }
		if err := tx.Model(&nodeAgentTaskResultQuarantineRow{}).Select("task_id, agent_id").
			Where("agent_id = ? AND task_id IN ?", agentID, ownIDs).Find(&evidence).Error; err != nil {
			return err
		}
		fenced := make(map[string]bool, len(evidence))
		for _, q := range evidence {
			if q.AgentID != agentID || !knownIDs[q.TaskID] {
				return fmt.Errorf("%w: quarantined result identity does not match exactly", domain.ErrConflict)
			}
			fenced[q.TaskID] = true
		}
		bounded := make([]nodeAgentTaskRow, 0, len(rows))
		totalArgs := 0
		encodedTaskArrayBytes := len("[]")
		for i := range rows {
			row := &rows[i]
			if row.AgentID != agentID || !knownIDs[row.TaskID] {
				return fmt.Errorf("%w: stored task identity does not match exactly", domain.ErrConflict)
			}
			if row.Lifecycle != nil && row.Lifecycle.NotAfterMS <= offeredAt.UnixMilli() {
				// Expiry closes authorization to dispatch, not our knowledge of
				// execution. Offered work may be running; queued work stays queued.
				// Both continue consuming active quota until an actual result or
				// explicit future reconciliation establishes a terminal outcome.
				updated := tx.Model(&nodeAgentTaskRow{}).
					Where("agent_id = ? AND task_id = ? AND status = ? AND dispatch_closed_at IS NULL", agentID, row.TaskID, row.Status).
					Updates(map[string]any{"dispatch_closed_at": offeredAt, "dispatch_closed_reason": "task_authorization_expired"})
				if updated.Error != nil {
					return updated.Error
				}
				if updated.RowsAffected != 1 {
					return errors.New("expire native agent task dispatch: state changed concurrently")
				}
				continue
			}
			if fenced[row.TaskID] || !kinds[row.Kind] || (row.Lifecycle != nil && !support.SupportsExpiry) ||
				len(bounded) >= limit || maxTaskJSONBytes < len("[]") {
				continue
			}
			wire := nodeAgentTaskToWire(row)
			if err := nodeprotocol.ValidateTasks([]nodeprotocol.Task{wire}); err != nil {
				return fmt.Errorf("offer native agent task %q: invalid stored task: %w", rows[i].TaskID, err)
			}
			if len(rows[i].Args) > nodeprotocol.MaxTaskArgsBytesPerResponse-totalArgs {
				continue
			}
			encoded, err := json.Marshal(wire)
			if err != nil {
				return fmt.Errorf("offer native agent task %q: encode: %w", rows[i].TaskID, err)
			}
			separatorBytes := 0
			if len(bounded) != 0 {
				separatorBytes = len(",")
			}
			if len(encoded)+separatorBytes > maxTaskJSONBytes-encodedTaskArrayBytes {
				continue
			}
			totalArgs += len(rows[i].Args)
			encodedTaskArrayBytes += len(encoded) + separatorBytes
			bounded = append(bounded, rows[i])
		}
		rows = bounded
		wireTasks := make([]nodeprotocol.Task, len(rows))
		for i := range rows {
			wireTasks[i] = nodeAgentTaskToWire(&rows[i])
		}
		if err := nodeprotocol.ValidateTasks(wireTasks); err != nil {
			return fmt.Errorf("offer native agent tasks: invalid batch: %w", err)
		}
		for i := range rows {
			updates := map[string]any{
				"last_offered_at": offeredAt,
				"offer_count":     gorm.Expr("offer_count + 1"),
			}
			if domain.NodeAgentTaskStatus(rows[i].Status) == domain.NodeAgentTaskQueued {
				updates["status"] = string(domain.NodeAgentTaskOffered)
				updates["first_offered_at"] = offeredAt
			}
			result := tx.Model(&nodeAgentTaskRow{}).
				Where("agent_id = ? AND task_id = ? AND status IN ? AND dispatch_closed_at IS NULL", agentID, rows[i].TaskID,
					[]string{string(domain.NodeAgentTaskQueued), string(domain.NodeAgentTaskOffered)}).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("offer native agent task %q: state changed concurrently", rows[i].TaskID)
			}
			rows[i].Status = string(domain.NodeAgentTaskOffered)
			rows[i].OfferCount++
			rows[i].LastOfferedAt = cloneTimePointer(&offeredAt)
			if rows[i].FirstOfferedAt == nil {
				rows[i].FirstOfferedAt = cloneTimePointer(&offeredAt)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]*domain.NodeAgentTask, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

func (r *nodeAgentTaskRepo) CompleteBatch(ctx context.Context, agentID string, results []domain.NodeAgentTaskResult, completedAt time.Time) error {
	if agentID == "" {
		return fmt.Errorf("%w: agent ID is required", domain.ErrValidation)
	}
	if len(results) == 0 {
		return nil
	}
	if completedAt.IsZero() {
		return fmt.Errorf("%w: completion time is required", domain.ErrValidation)
	}
	ordered := append([]domain.NodeAgentTaskResult(nil), results...)
	wireResults := make([]nodeprotocol.TaskResult, len(ordered))
	for i := range ordered {
		ordered[i].Result = append([]byte(nil), ordered[i].Result...)
		if ordered[i].Kind == "" || ordered[i].InputSHA256 == "" {
			return fmt.Errorf("%w: durable task result identity is required", domain.ErrValidation)
		}
		wireResults[i] = taskResultToWire(ordered[i])
	}
	if err := nodeprotocol.ValidateTaskResults(wireResults); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].TaskID < ordered[j].TaskID })
	for i := 1; i < len(ordered); i++ {
		if ordered[i-1].TaskID == ordered[i].TaskID {
			return fmt.Errorf("%w: duplicate task result %q", domain.ErrValidation, ordered[i].TaskID)
		}
	}
	ids := make([]string, len(ordered))
	// Canonicalize once before the transaction. Exact evidence replay compares
	// this wire representation (nil and empty omitted byte slices are equal).
	incomingEvidence := make(map[string]*nodeAgentTaskResultQuarantineRow, len(ordered))
	for i := range ordered {
		ids[i] = ordered[i].TaskID
		evidence, err := newNodeAgentTaskQuarantine(agentID, ordered[i], domain.NodeAgentTaskQuarantineUnknownTask, completedAt.UTC())
		if err != nil {
			return err
		}
		incomingEvidence[ordered[i].TaskID] = evidence
	}
	completedAt = completedAt.UTC()
	err := runTransactionWithRetry(ctx, r.db, func(tx *gorm.DB) error {
		// GORM's ordinary slow/error SQL log interpolates bound values. Result
		// and quarantine writes contain private opaque evidence, so this narrow
		// transaction suppresses SQL tracing; safe repository errors still escape.
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		if _, err := lockNodeAgentByAgentID(tx, agentID); err != nil {
			return err
		}
		// Resolve only public identity first. InnoDB may otherwise choose the
		// global primary key for a locking task_id IN query and lock foreign
		// records before applying its agent_id predicate. Unknown/foreign IDs
		// must never enter the full-row locking query below.
		var ownership []struct{ TaskID, AgentID string }
		if err := tx.Model(&nodeAgentTaskRow{}).Select("task_id, agent_id").Where("task_id IN ?", ids).Find(&ownership).Error; err != nil {
			return err
		}
		ownIDs := make([]string, 0, len(ownership))
		for _, owner := range ownership {
			if owner.AgentID != agentID || incomingEvidence[owner.TaskID] == nil {
				return fmt.Errorf("%w: task result belongs to another stored identity", domain.ErrConflict)
			}
			ownIDs = append(ownIDs, owner.TaskID)
		}
		var rows []nodeAgentTaskRow
		if len(ownIDs) != 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND task_id IN ?", agentID, ownIDs).
				Order("task_id ASC").Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) != len(ownIDs) {
				return fmt.Errorf("%w: stored task ownership changed concurrently", domain.ErrConflict)
			}
		}
		ownByID := make(map[string]*nodeAgentTaskRow, len(rows))
		for i := range rows {
			if rows[i].AgentID != agentID || incomingEvidence[rows[i].TaskID] == nil {
				return fmt.Errorf("%w: task result stored identity does not match exactly", domain.ErrConflict)
			}
			ownByID[rows[i].TaskID] = &rows[i]
		}
		var quarantines []nodeAgentTaskResultQuarantineRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND task_id IN ?", agentID, ids).Order("task_id ASC").Find(&quarantines).Error; err != nil {
			return err
		}
		existingEvidence := make(map[string]*nodeAgentTaskResultQuarantineRow, len(quarantines))
		for i := range quarantines {
			q := &quarantines[i]
			incoming := incomingEvidence[q.TaskID]
			if q.AgentID != agentID || incoming == nil {
				return fmt.Errorf("%w: quarantined result stored identity does not match exactly", domain.ErrConflict)
			}
			if _, err := decodeNodeAgentTaskQuarantine(q); err != nil {
				return err
			}
			if !bytes.Equal(q.Payload, incoming.Payload) {
				return fmt.Errorf("%w: task result conflicts with quarantined evidence", domain.ErrConflict)
			}
			existingEvidence[q.TaskID] = q
		}

		// Resolve every conflict and gather only new evidence before the first
		// write. Equal replay bypasses admission even when the quota is full.
		newEvidence := make([]*nodeAgentTaskResultQuarantineRow, 0, len(ordered))
		var newPayloadBytes int64
		for _, result := range ordered {
			row := ownByID[result.TaskID]
			needsEvidence := row == nil
			if row != nil && (row.Kind != result.Kind || row.InputSHA256 != result.InputSHA256 || nodeAgentTaskNotAfterMS(row) != result.NotAfterMS) {
				return fmt.Errorf("%w: task result identity conflicts with offered input", domain.ErrConflict)
			}
			if row != nil {
				status := domain.NodeAgentTaskStatus(row.Status)
				switch {
				case status == domain.NodeAgentTaskQueued:
					needsEvidence = true
				case status.Terminal():
					if !sameTerminalResult(row, result) {
						return fmt.Errorf("%w: task result conflicts with the stored terminal result", domain.ErrConflict)
					}
				case status != domain.NodeAgentTaskOffered:
					return fmt.Errorf("%w: task has invalid stored status", domain.ErrConflict)
				}
			}
			if needsEvidence && existingEvidence[result.TaskID] == nil {
				q := *incomingEvidence[result.TaskID]
				if row != nil {
					q.Reason = string(domain.NodeAgentTaskQuarantineNeverOffered)
				}
				newEvidence = append(newEvidence, &q)
				newPayloadBytes += int64(len(q.Payload)) // shared report limits bound this sum
			}
		}
		if err := r.enforceQuarantineQuota(tx, agentID, int64(len(newEvidence)), newPayloadBytes); err != nil {
			return err
		}
		for _, q := range newEvidence {
			if err := tx.Create(q).Error; err != nil {
				return err
			}
		}
		for _, q := range existingEvidence {
			if completedAt.After(q.LastSeenAt) {
				if err := tx.Model(&nodeAgentTaskResultQuarantineRow{}).
					Where("agent_id = ? AND task_id = ?", agentID, q.TaskID).
					Update("last_seen_at", completedAt).Error; err != nil {
					return err
				}
			}
		}

		for _, result := range ordered {
			row := ownByID[result.TaskID]
			if row == nil || domain.NodeAgentTaskStatus(row.Status).Terminal() {
				continue
			}
			if domain.NodeAgentTaskStatus(row.Status) == domain.NodeAgentTaskQueued || existingEvidence[row.TaskID] != nil {
				// Receipt evidence never automatically promotes a restored task to
				// terminal. Stop redispatch of matching nonterminal rows pending
				// reconciliation, even if a backup restored them as offered.
				if row.DispatchClosedAt == nil {
					reason := string(domain.NodeAgentTaskQuarantineNeverOffered)
					if q := existingEvidence[row.TaskID]; q != nil {
						reason = q.Reason
					}
					updated := tx.Model(&nodeAgentTaskRow{}).
						Where("agent_id = ? AND task_id = ? AND status = ? AND dispatch_closed_at IS NULL", agentID, row.TaskID, row.Status).
						Updates(map[string]any{"dispatch_closed_at": completedAt, "dispatch_closed_reason": reason})
					if updated.Error != nil {
						return updated.Error
					}
					if updated.RowsAffected != 1 {
						return errors.New("close native agent task dispatch: state changed concurrently")
					}
				}
				continue
			}
			ok := result.OK
			status := resultStatus(result)
			updated := tx.Model(&nodeAgentTaskRow{}).
				Where("agent_id = ? AND task_id = ? AND status = ?", agentID, row.TaskID, string(domain.NodeAgentTaskOffered)).
				Updates(map[string]any{
					"status":               string(status),
					"result_ok":            &ok,
					"result_indeterminate": result.Indeterminate,
					"result":               append([]byte(nil), result.Result...),
					"result_error_code":    result.ErrorCode,
					"result_error":         result.Error,
					"completed_at":         completedAt,
				})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return fmt.Errorf("complete native agent task %q: state changed concurrently", row.TaskID)
			}
		}
		return nil
	})
	return safeNodeAgentTaskResultStorageError(err)
}

func validateNewNodeAgentTask(task *domain.NodeAgentTask) error {
	if task == nil || task.TaskID == "" || task.AgentID == "" || task.Kind == "" {
		return fmt.Errorf("%w: task ID, agent ID, and kind are required", domain.ErrValidation)
	}
	if len(task.AgentID) > 64 || len(task.SupersedesTaskID) > nodeprotocol.MaxTaskIDBytes {
		return fmt.Errorf("%w: native agent task exceeds field size limits", domain.ErrValidation)
	}
	if task.Status == "" {
		task.Status = domain.NodeAgentTaskQueued
	}
	if task.Lifecycle != nil {
		if err := task.Lifecycle.Validate(); err != nil {
			return err
		}
	}
	if task.Status != domain.NodeAgentTaskQueued || task.ResultOK != nil || task.ResultIndeterminate || len(task.Result) != 0 ||
		task.ResultErrorCode != "" || task.ResultError != "" || task.CompletedAt != nil ||
		task.OfferCount != 0 || task.FirstOfferedAt != nil || task.LastOfferedAt != nil ||
		task.DispatchClosedAt != nil || task.DispatchClosedReason != "" {
		return fmt.Errorf("%w: a new native agent task must be pristine and queued", domain.ErrValidation)
	}
	var notAfterMS int64
	if task.Lifecycle != nil {
		notAfterMS = task.Lifecycle.NotAfterMS
	}
	if err := nodeprotocol.ValidateTasks([]nodeprotocol.Task{{
		ID: task.TaskID, Kind: task.Kind, Args: task.Args, InputSHA256: task.InputSHA256,
		NotAfterMS: notAfterMS,
	}}); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if task.IdempotencyKeySHA256 != nil {
		normalized := strings.ToLower(*task.IdempotencyKeySHA256)
		if !validSHA256Hex(normalized) {
			return fmt.Errorf("%w: idempotency key must be a SHA-256 hex digest", domain.ErrValidation)
		}
		task.IdempotencyKeySHA256 = &normalized
	}
	return nil
}

func sameTaskRequest(stored, incoming *nodeAgentTaskRow, matchedByTaskID bool) bool {
	if stored.AgentID != incoming.AgentID || stored.Kind != incoming.Kind ||
		stored.InputSHA256 != incoming.InputSHA256 || !bytes.Equal(stored.Args, incoming.Args) ||
		stored.SupersedesTaskID != incoming.SupersedesTaskID ||
		!equalStringPointers(stored.IdempotencyKeySHA256, incoming.IdempotencyKeySHA256) {
		return false
	}
	if !matchedByTaskID {
		// A fresh-ID idempotency alias identifies the same logical input. Return
		// the original snapshot, including legacy nil: a caller's recomputed
		// deadline or changed settings must not extend that existing task.
		return true
	}
	if stored.TaskID != incoming.TaskID {
		return false
	}
	if stored.Lifecycle == nil || incoming.Lifecycle == nil {
		return stored.Lifecycle == nil && incoming.Lifecycle == nil
	}
	return *stored.Lifecycle == *incoming.Lifecycle
}

func sameTerminalResult(stored *nodeAgentTaskRow, result domain.NodeAgentTaskResult) bool {
	return nodeAgentTaskNotAfterMS(stored) == result.NotAfterMS &&
		domain.NodeAgentTaskStatus(stored.Status) == resultStatus(result) &&
		stored.ResultOK != nil && *stored.ResultOK == result.OK && stored.ResultIndeterminate == result.Indeterminate &&
		bytes.Equal(stored.Result, result.Result) && stored.ResultErrorCode == result.ErrorCode && stored.ResultError == result.Error
}

func nodeAgentTaskNotAfterMS(task *nodeAgentTaskRow) int64 {
	if task.Lifecycle == nil {
		return 0
	}
	return task.Lifecycle.NotAfterMS
}

func nodeAgentTaskToWire(task *nodeAgentTaskRow) nodeprotocol.Task {
	return nodeprotocol.Task{
		ID: task.TaskID, Kind: task.Kind, Args: task.Args,
		InputSHA256: task.InputSHA256, NotAfterMS: nodeAgentTaskNotAfterMS(task),
	}
}

func resultStatus(result domain.NodeAgentTaskResult) domain.NodeAgentTaskStatus {
	if result.Indeterminate {
		return domain.NodeAgentTaskIndeterminate
	}
	if result.OK {
		return domain.NodeAgentTaskSucceeded
	}
	return domain.NodeAgentTaskFailed
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func equalStringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ ports.NodeAgentTaskRepo = (*nodeAgentTaskRepo)(nil)
