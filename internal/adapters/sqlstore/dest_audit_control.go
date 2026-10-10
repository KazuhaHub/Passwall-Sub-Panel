package sqlstore

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

func (r *DestAuditRepo) WatchDestinationAuditControls(ctx context.Context, observer func([]domain.DestAuditControl)) error {
	if observer == nil {
		return domain.ErrValidation
	}
	r.observerMu.Lock()
	defer r.observerMu.Unlock()
	if r.observer != nil {
		return domain.ErrConflict
	}
	// Writers release their DB transaction before taking observerMu. A seed
	// may wait for their commit; they then deliver any change after this read.
	var rows []xuiPanelRow
	if err := r.privateDB(ctx).Select("id", "kind", "audit_collect", "audit_collect_revision").Where("kind = ?", string(domain.PanelKindPSP)).Order("id").Find(&rows).Error; err != nil {
		return auditStorageError(err)
	}
	states := make([]domain.DestAuditControl, len(rows))
	for i, row := range rows {
		state := auditControlState(row)
		if !state.Available {
			return domain.ErrUnavailable
		}
		states[i] = state
	}
	observer(states)
	r.observer = observer
	return nil
}

func auditControlState(row xuiPanelRow) domain.DestAuditControl {
	state := domain.DestAuditControl{PanelID: row.ID}
	collect := domain.AuditCollect(row.AuditCollect)
	if row.ID > 0 && domain.NormalizePanelKind(domain.PanelKind(row.Kind)) == domain.PanelKindPSP && collect.Valid() && row.AuditCollectRevision >= 1 {
		state.Available, state.Collect, state.Revision = true, collect, uint64(row.AuditCollectRevision)
	}
	return state
}

func (r *DestAuditRepo) lockControlPanel(panelID int64) func() {
	if r == nil || r.gates == nil {
		return func() {}
	}
	return r.gates.Lock(panelID)
}

// Call only after commit, while the shared panel gate is still held. The
// callback must only update memory and cannot re-enter a collection writer.
func (r *DestAuditRepo) notifyControl(state domain.DestAuditControl) {
	if r == nil {
		return
	}
	r.observerMu.Lock()
	defer r.observerMu.Unlock()
	if r.observer != nil {
		r.observer([]domain.DestAuditControl{state})
	}
}

// Creation learns the panel ID inside its transaction. Re-read after taking
// the panel gate rather than publishing the pre-commit object: a newer setting
// or a deletion may already have committed before this late creation notice.
// Failed reads invalidate that cache entry without changing a committed CRUD
// result into an error that encourages a duplicate creation.
func (r *DestAuditRepo) notifyCurrentControl(ctx context.Context, panelID int64) {
	if r == nil {
		return
	}
	r.observerMu.Lock()
	listening := r.observer != nil
	r.observerMu.Unlock()
	if !listening {
		return
	}
	state := domain.DestAuditControl{PanelID: panelID}
	var rows []xuiPanelRow
	if err := r.privateDB(ctx).Select("id", "kind", "audit_collect", "audit_collect_revision").Where("id = ?", panelID).Limit(1).Find(&rows).Error; err != nil {
		log.Warn("destination audit collection cache invalidated", "panel_id", panelID, "reason", "storage_unavailable")
	} else if len(rows) == 1 {
		state = auditControlState(rows[0])
	}
	r.notifyControl(state)
}
