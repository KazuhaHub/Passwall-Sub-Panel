package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Composite identity is deliberate: an untrusted agent must not reserve a
// different agent's existing or future task IDs by sending guessed results.
type nodeAgentTaskResultQuarantineRow struct {
	AgentID       string    `gorm:"primaryKey;size:64;not null"`
	TaskID        string    `gorm:"primaryKey;size:128;not null"`
	Payload       []byte    `gorm:"not null"`
	PayloadSHA256 string    `gorm:"size:64;not null"`
	Reason        string    `gorm:"size:32;not null;check:chk_node_agent_task_quarantine_reason,reason IN ('unknown_task','never_offered')"`
	FirstSeenAt   time.Time `gorm:"not null"`
	LastSeenAt    time.Time `gorm:"not null"`
}

func (nodeAgentTaskResultQuarantineRow) TableName() string {
	return "node_agent_task_result_quarantines"
}

// These compiled caps bound unreviewed evidence independently of the active
// task backlog. There is no automatic retention or loss of evidence here.
const (
	defaultMaxNodeAgentTaskQuarantineRows  int64 = 256
	defaultMaxNodeAgentTaskQuarantineBytes int64 = 16 << 20
)

type nodeAgentTaskQuarantineQuota struct {
	MaxRows         int64
	MaxPayloadBytes int64
}

func defaultNodeAgentTaskQuarantineQuota() nodeAgentTaskQuarantineQuota {
	return nodeAgentTaskQuarantineQuota{
		MaxRows:         defaultMaxNodeAgentTaskQuarantineRows,
		MaxPayloadBytes: defaultMaxNodeAgentTaskQuarantineBytes,
	}
}

type nodeAgentTaskQuarantineUsage struct {
	Rows         int64 `gorm:"column:quarantine_rows"`
	PayloadBytes int64 `gorm:"column:quarantine_payload_bytes"`
}

func (r *nodeAgentTaskRepo) enforceQuarantineQuota(tx *gorm.DB, agentID string, requestedRows, requestedBytes int64) error {
	if requestedRows == 0 {
		return nil
	}
	var usage nodeAgentTaskQuarantineUsage
	if err := tx.Model(&nodeAgentTaskResultQuarantineRow{}).
		Select("COUNT(*) AS quarantine_rows, COALESCE(SUM(LENGTH(payload)), 0) AS quarantine_payload_bytes").
		Where("agent_id = ?", agentID).Scan(&usage).Error; err != nil {
		return fmt.Errorf("measure native agent task quarantine quota: %w", err)
	}
	return checkNodeAgentTaskQuarantineQuota(r.quarantineQuota, usage, requestedRows, requestedBytes)
}

func checkNodeAgentTaskQuarantineQuota(quota nodeAgentTaskQuarantineQuota, usage nodeAgentTaskQuarantineUsage, requestedRows, requestedBytes int64) error {
	if quota.MaxRows <= 0 || quota.MaxPayloadBytes <= 0 {
		return fmt.Errorf("%w: native agent task quarantine quota is not positive", domain.ErrResourceExhausted)
	}
	if usage.Rows < 0 || usage.PayloadBytes < 0 || requestedRows < 0 || requestedBytes < 0 {
		return errors.New("native agent task quarantine quota contains a negative value")
	}
	if usage.Rows > quota.MaxRows || requestedRows > quota.MaxRows-usage.Rows {
		return fmt.Errorf("%w: native agent task quarantine rows used=%d requested=%d limit=%d",
			domain.ErrResourceExhausted, usage.Rows, requestedRows, quota.MaxRows)
	}
	if usage.PayloadBytes > quota.MaxPayloadBytes || requestedBytes > quota.MaxPayloadBytes-usage.PayloadBytes {
		return fmt.Errorf("%w: native agent task quarantine bytes used=%d requested=%d limit=%d",
			domain.ErrResourceExhausted, usage.PayloadBytes, requestedBytes, quota.MaxPayloadBytes)
	}
	return nil
}

func (r *nodeAgentTaskRepo) GetQuarantinedResult(ctx context.Context, agentID, taskID string) (*domain.NodeAgentTaskResultQuarantine, error) {
	if agentID == "" || taskID == "" {
		return nil, fmt.Errorf("%w: agent and task IDs are required", domain.ErrValidation)
	}
	var row nodeAgentTaskResultQuarantineRow
	if err := r.db.WithContext(ctx).Where("agent_id = ? AND task_id = ?", agentID, taskID).First(&row).Error; err != nil {
		return nil, safeNodeAgentTaskResultStorageError(wrapNotFound(err))
	}
	// Do not inherit a MySQL collation's case folding as wire identity.
	if row.AgentID != agentID || row.TaskID != taskID {
		return nil, fmt.Errorf("%w: native agent task quarantine", domain.ErrNotFound)
	}
	return decodeNodeAgentTaskQuarantine(&row)
}

// Keep the cause for errors.Is/As and transaction retry diagnostics without
// allowing driver diagnostics (which may echo rejected values) into logs.
type nodeAgentTaskResultStorageError struct{ cause error }

func (e *nodeAgentTaskResultStorageError) Error() string {
	return "native agent task result storage failed"
}

func (e *nodeAgentTaskResultStorageError) Unwrap() error { return e.cause }

func safeNodeAgentTaskResultStorageError(err error) error {
	if err == nil || errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrConflict) ||
		errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrResourceExhausted) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &nodeAgentTaskResultStorageError{cause: err}
}

func taskResultToWire(result domain.NodeAgentTaskResult) nodeprotocol.TaskResult {
	return nodeprotocol.TaskResult{
		ID: result.TaskID, Kind: result.Kind, InputSHA256: result.InputSHA256,
		NotAfterMS: result.NotAfterMS,
		OK:         result.OK, Indeterminate: result.Indeterminate, Result: result.Result,
		ErrorCode: result.ErrorCode, Error: result.Error,
	}
}

func taskResultFromWire(result nodeprotocol.TaskResult) domain.NodeAgentTaskResult {
	return domain.NodeAgentTaskResult{
		TaskID: result.ID, Kind: result.Kind, InputSHA256: result.InputSHA256,
		NotAfterMS: result.NotAfterMS,
		OK:         result.OK, Indeterminate: result.Indeterminate, Result: append([]byte(nil), result.Result...),
		ErrorCode: result.ErrorCode, Error: result.Error,
	}
}

func newNodeAgentTaskQuarantine(agentID string, result domain.NodeAgentTaskResult, reason domain.NodeAgentTaskQuarantineReason, seenAt time.Time) (*nodeAgentTaskResultQuarantineRow, error) {
	payload, err := json.Marshal(taskResultToWire(result))
	if err != nil {
		return nil, errors.New("encode native agent task quarantine evidence")
	}
	hash := sha256.Sum256(payload)
	return &nodeAgentTaskResultQuarantineRow{
		AgentID: agentID, TaskID: result.TaskID, Payload: payload, PayloadSHA256: hex.EncodeToString(hash[:]),
		Reason: string(reason), FirstSeenAt: seenAt, LastSeenAt: seenAt,
	}, nil
}

// Errors intentionally omit raw payload, Result and Error text. Handlers log
// repository errors, and even corrupted evidence can contain private material.
func decodeNodeAgentTaskQuarantine(row *nodeAgentTaskResultQuarantineRow) (*domain.NodeAgentTaskResultQuarantine, error) {
	corrupt := func() (*domain.NodeAgentTaskResultQuarantine, error) {
		return nil, errors.New("native agent task quarantine evidence failed integrity validation")
	}
	hash := sha256.Sum256(row.Payload)
	if !validSHA256Hex(row.PayloadSHA256) || row.PayloadSHA256 != hex.EncodeToString(hash[:]) {
		return corrupt()
	}
	var wire nodeprotocol.TaskResult
	if err := json.Unmarshal(row.Payload, &wire); err != nil || wire.ID != row.TaskID || wire.Kind == "" || wire.InputSHA256 == "" {
		return corrupt()
	}
	if err := nodeprotocol.ValidateTaskResults([]nodeprotocol.TaskResult{wire}); err != nil {
		return corrupt()
	}
	canonical, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(canonical, row.Payload) {
		return corrupt()
	}
	reason := domain.NodeAgentTaskQuarantineReason(row.Reason)
	if reason != domain.NodeAgentTaskQuarantineUnknownTask && reason != domain.NodeAgentTaskQuarantineNeverOffered {
		return corrupt()
	}
	if row.AgentID == "" || row.FirstSeenAt.IsZero() || row.LastSeenAt.Before(row.FirstSeenAt) {
		return corrupt()
	}
	return &domain.NodeAgentTaskResultQuarantine{
		AgentID: row.AgentID, TaskID: row.TaskID, Payload: append([]byte(nil), row.Payload...),
		PayloadSHA256: row.PayloadSHA256, Reason: reason, Result: taskResultFromWire(wire),
		FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
	}, nil
}
