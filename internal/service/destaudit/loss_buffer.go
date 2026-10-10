package destaudit

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"sync"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const lossBufferKeys = 10000
const lossFlushKeys = 200

type lossFlushResult struct {
	keys      int
	rows      int64
	discarded bool
}
type pendingLoss struct {
	rows int64
}
type lossBuffer struct {
	mu      sync.Mutex
	flushMu sync.Mutex
	slots   map[domain.DestAuditLoss]*pendingLoss // Rows is zero in each key.
	frozen  *domain.DestAuditLossBatch
}

func newLossBuffer() *lossBuffer {
	return &lossBuffer{slots: make(map[domain.DestAuditLoss]*pendingLoss)}
}

func (b *lossBuffer) pendingKeys() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.slots)
}

// The producer only takes a short memory lock, never the flush/storage lock.
// Frozen keys retain their slots; new increments on those keys stay separate.
func (b *lossBuffer) add(loss domain.DestAuditLoss) bool {
	if !validBufferedLoss(loss) {
		return false
	}
	rows := loss.Rows
	loss.Rows = 0
	b.mu.Lock()
	defer b.mu.Unlock()
	slot := b.slots[loss]
	if slot == nil {
		if len(b.slots) >= lossBufferKeys {
			return false
		}
		slot = &pendingLoss{}
		b.slots[loss] = slot
	}
	slot.rows = addLossRows(slot.rows, rows)
	return true
}

func validBufferedLoss(loss domain.DestAuditLoss) bool {
	if loss.PanelID <= 0 || loss.HourMS <= 0 || loss.HourMS%protocol.AuditHourMS != 0 || loss.Rows <= 0 {
		return false
	}
	switch loss.Kind {
	case "block", "observe", "trial", "usage":
	default:
		return false
	}
	switch loss.Reason {
	case "queue_full", "collect_off", "stale_collect_revision", "ingest_error", "unknown_subject", "out_of_range", "over_budget":
		return true
	default:
		return false
	}
}

func addLossRows(a, n int64) int64 {
	if n > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + n
}

func (b *lossBuffer) flush(ctx context.Context, repo ports.DestAuditLossRepo, now time.Time) (lossFlushResult, error) {
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
	if now.IsZero() {
		return lossFlushResult{}, domain.ErrValidation
	}
	batch := b.snapshot(now)
	if batch == nil {
		return lossFlushResult{}, nil
	}
	result := lossFlushResult{keys: len(batch.Losses)}
	for _, row := range batch.Losses {
		result.rows = addLossRows(result.rows, row.Rows)
	}
	err := repo.FlushDestinationAuditLoss(ctx, *batch)
	if err == nil || errors.Is(err, domain.ErrDestAuditLossExpired) {
		b.acknowledge()
		result.discarded = err != nil
	}
	return result, err
}

func (b *lossBuffer) snapshot(now time.Time) *domain.DestAuditLossBatch {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.frozen == nil {
		keys := make([]domain.DestAuditLoss, 0, len(b.slots))
		for key, slot := range b.slots {
			if slot.rows > 0 {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			return nil
		}
		slices.SortFunc(keys, func(a, c domain.DestAuditLoss) int {
			if n := cmp.Compare(a.HourMS, c.HourMS); n != 0 {
				return n
			}
			if n := cmp.Compare(a.PanelID, c.PanelID); n != 0 {
				return n
			}
			if n := cmp.Compare(a.Kind, c.Kind); n != 0 {
				return n
			}
			return cmp.Compare(a.Reason, c.Reason)
		})
		var id [16]byte
		_, _ = rand.Read(id[:])
		b.frozen = &domain.DestAuditLossBatch{BatchID: hex.EncodeToString(id[:]), ReceivedAt: now.UTC(), Losses: make([]domain.DestAuditLoss, 0, min(len(keys), lossFlushKeys))}
		for _, key := range keys[:min(len(keys), lossFlushKeys)] {
			slot := b.slots[key]
			row := key
			row.Rows = slot.rows
			slot.rows = 0
			b.frozen.Losses = append(b.frozen.Losses, row)
		}
	}
	batch := *b.frozen
	batch.Losses = slices.Clone(batch.Losses)
	return &batch
}

func (b *lossBuffer) acknowledge() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, row := range b.frozen.Losses {
		row.Rows = 0
		if b.slots[row].rows == 0 {
			delete(b.slots, row)
		}
	}
	b.frozen = nil
}

// The worker calls this only after its last storage operation has returned.
// Count a key once even when it has both frozen and newly added increments.
func (b *lossBuffer) discard() lossFlushResult {
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	result := lossFlushResult{keys: len(b.slots), discarded: true}
	for _, slot := range b.slots {
		result.rows = addLossRows(result.rows, slot.rows)
	}
	if b.frozen != nil {
		for _, row := range b.frozen.Losses {
			result.rows = addLossRows(result.rows, row.Rows)
		}
	}
	clear(b.slots)
	b.frozen = nil
	return result
}
