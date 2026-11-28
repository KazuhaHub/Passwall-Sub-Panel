package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// pspClientRow is the v3.9.0 first-class client table (domain.PSPClient). ID is
// the only durable identity. Email and CredClass describe the current upstream
// projection and may change when the rules domain or partition layout changes.
// The non-unique (panel_id,email) index keeps upstream observation lookups fast
// without letting a rendered email decide whether a local row survives.
type pspClientRow struct {
	ID     int64 `gorm:"primaryKey;autoIncrement"`
	UserID int64 `gorm:"index;not null;uniqueIndex:uk_psp_client_line_panel,priority:1"`
	// NodeID / LineID: the subscription line this row is the client of (ADR
	// 0043), unique per user and server (panel_id is the second key column: a
	// tunnel-mode line's client lives on the entry server, not the landing's, so
	// switching a line's mode moves its identity to another server, while the old
	// server's row waits, detached, for its counters to be booked). NULL on a
	// legacy row (several NULLs never collide in a unique index on any of the
	// three databases).
	NodeID *int64  `gorm:"uniqueIndex:uk_psp_client_line_panel,priority:3"`
	LineID *string `gorm:"size:128;uniqueIndex:uk_psp_client_line_panel,priority:4"`
	// UUIDKey is the UUID in domain.LineUUIDKey form, indexed so a derived
	// line identity can be checked for uniqueness on its listener.
	UUIDKey string `gorm:"column:uuid_key;size:32;not null;default:'';index"`
	// LastFactorMilli: the line's traffic factor at the last booking.
	LastFactorMilli *int   `gorm:"not null;default:1000"`
	PanelID         int64  `gorm:"not null;index:idx_psp_client_panel_email,priority:1;uniqueIndex:uk_psp_client_line_panel,priority:2"`
	Email           string `gorm:"size:255;not null;index:idx_psp_client_panel_email,priority:2"`
	CredClass       int    `gorm:"not null;default:0"`
	UUID            string `gorm:"size:36;not null;default:''"`
	Password        string `gorm:"size:128;not null;default:''"`

	DesiredEnable      bool  `gorm:"not null;default:false"`
	DesiredExpiryTime  int64 `gorm:"not null;default:0"`
	PanelQuotaHeadroom int64 `gorm:"not null;default:0"`
	PanelIPLimit       int   `gorm:"not null;default:0"`
	PanelDeviceLimit   int   `gorm:"not null;default:0"`
	DesiredMinted      bool  `gorm:"not null;default:false"`
	CreatedAt          time.Time

	LifetimeUpBytes    int64 `gorm:"default:0"`
	LifetimeDownBytes  int64 `gorm:"default:0"`
	LifetimeTotalBytes int64 `gorm:"default:0"`

	LastRawUpBytes    int64  `gorm:"default:0"`
	LastRawDownBytes  int64  `gorm:"default:0"`
	LastRawTotalBytes int64  `gorm:"default:0"`
	LastCounterEpoch  uint64 `gorm:"default:0"`

	PeriodBaselineUpBytes    int64 `gorm:"default:0"`
	PeriodBaselineDownBytes  int64 `gorm:"default:0"`
	PeriodBaselineTotalBytes int64 `gorm:"default:0"`

	// ChargedLifetimeBytes: lifetime_total_bytes weighted by the server's
	// traffic factor, delta by delta. The other counters stay raw.
	ChargedLifetimeBytes int64 `gorm:"not null;default:0"`
}

func (pspClientRow) TableName() string { return "psp_clients" }

// pspClientInboundRow is the attachment junction (domain.PSPClientInbound):
// which inbounds (PSP nodes) a client is attached to, unique per (client, node).
type pspClientInboundRow struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	ClientID        int64  `gorm:"not null;index;uniqueIndex:uk_psp_client_inbound,priority:1"`
	NodeID          int64  `gorm:"not null;uniqueIndex:uk_psp_client_inbound,priority:2"`
	FlowOverride    string `gorm:"size:64;not null;default:''"`
	State           string `gorm:"size:16;not null;default:pending"`
	AppliedVersion  uint64 `gorm:"not null;default:0"`
	AppliedEmail    string `gorm:"size:255;not null;default:''"`
	AppliedUUID     string `gorm:"size:36;not null;default:''"`
	AppliedPassword string `gorm:"size:128;not null;default:''"`
	FirstFailedAt   *time.Time
}

func (pspClientInboundRow) TableName() string { return "psp_client_inbounds" }

func pspClientToRow(c *domain.PSPClient) pspClientRow {
	return pspClientRow{
		ID:                       c.ID,
		UserID:                   c.UserID,
		PanelID:                  c.PanelID,
		Email:                    c.Email,
		CredClass:                c.CredClass,
		UUID:                     c.UUID,
		Password:                 c.Password,
		DesiredEnable:            c.DesiredEnable,
		DesiredExpiryTime:        c.DesiredExpiryTime,
		PanelQuotaHeadroom:       c.PanelQuotaHeadroom,
		PanelIPLimit:             c.PanelIPLimit,
		PanelDeviceLimit:         c.PanelDeviceLimit,
		DesiredMinted:            c.DesiredMinted,
		CreatedAt:                c.CreatedAt,
		LifetimeUpBytes:          c.LifetimeUpBytes,
		LifetimeDownBytes:        c.LifetimeDownBytes,
		LifetimeTotalBytes:       c.LifetimeTotalBytes,
		LastRawUpBytes:           c.LastRawUpBytes,
		LastRawDownBytes:         c.LastRawDownBytes,
		LastRawTotalBytes:        c.LastRawTotalBytes,
		LastCounterEpoch:         c.LastCounterEpoch,
		PeriodBaselineUpBytes:    c.PeriodBaselineUpBytes,
		PeriodBaselineDownBytes:  c.PeriodBaselineDownBytes,
		PeriodBaselineTotalBytes: c.PeriodBaselineTotalBytes,
		ChargedLifetimeBytes:     c.ChargedLifetimeBytes,
		NodeID:                   lineNodeColumn(c),
		LineID:                   lineIDColumn(c),
		UUIDKey:                  domain.LineUUIDKey(c.UUID),
		LastFactorMilli:          c.LastFactorMilli,
	}
}

// lineNodeColumn / lineIDColumn store a legacy row's missing line as NULL.
func lineNodeColumn(c *domain.PSPClient) *int64 {
	if _, ok := c.Line(); !ok {
		return nil
	}
	v := c.NodeID
	return &v
}

func lineIDColumn(c *domain.PSPClient) *string {
	if _, ok := c.Line(); !ok {
		return nil
	}
	v := c.LineID
	return &v
}

func rowToPSPClient(r *pspClientRow) *domain.PSPClient {
	return &domain.PSPClient{
		ID:                       r.ID,
		UserID:                   r.UserID,
		PanelID:                  r.PanelID,
		Email:                    r.Email,
		CredClass:                r.CredClass,
		UUID:                     r.UUID,
		Password:                 r.Password,
		DesiredEnable:            r.DesiredEnable,
		DesiredExpiryTime:        r.DesiredExpiryTime,
		PanelQuotaHeadroom:       r.PanelQuotaHeadroom,
		PanelIPLimit:             r.PanelIPLimit,
		PanelDeviceLimit:         r.PanelDeviceLimit,
		DesiredMinted:            r.DesiredMinted,
		CreatedAt:                r.CreatedAt,
		LifetimeUpBytes:          r.LifetimeUpBytes,
		LifetimeDownBytes:        r.LifetimeDownBytes,
		LifetimeTotalBytes:       r.LifetimeTotalBytes,
		LastRawUpBytes:           r.LastRawUpBytes,
		LastRawDownBytes:         r.LastRawDownBytes,
		LastRawTotalBytes:        r.LastRawTotalBytes,
		LastCounterEpoch:         r.LastCounterEpoch,
		PeriodBaselineUpBytes:    r.PeriodBaselineUpBytes,
		PeriodBaselineDownBytes:  r.PeriodBaselineDownBytes,
		PeriodBaselineTotalBytes: r.PeriodBaselineTotalBytes,
		ChargedLifetimeBytes:     r.ChargedLifetimeBytes,
		NodeID:                   derefInt64(r.NodeID),
		LineID:                   derefString(r.LineID),
		LastFactorMilli:          r.LastFactorMilli,
	}
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func (r *pspClientRepo) UpdateDesiredLifecycleByUser(ctx context.Context, userID int64, lifecycle domain.UserLifecycle) error {
	if userID == 0 {
		return errors.New("UpdateDesiredLifecycleByUser: user ID required")
	}
	return r.db.WithContext(ctx).Model(&pspClientRow{}).Where("user_id = ?", userID).Updates(map[string]any{
		"desired_enable":       lifecycle.Enable,
		"desired_expiry_time":  lifecycle.ExpiryTime,
		"panel_quota_headroom": lifecycle.QuotaHeadroom,
		"panel_ip_limit":       lifecycle.IPLimit,
		"panel_device_limit":   lifecycle.DeviceLimit,
		"desired_minted":       true,
	}).Error
}

type pspClientRepo struct{ db *gorm.DB }

// Create mints a new durable client identity. The caller must not supply an ID:
// an existing row is updated only through UpdateDefinition, whose required ID
// prevents email from ever becoming an accidental identity discriminator again.
// Initial counters are accepted for import/migration callers.
func (r *pspClientRepo) Create(ctx context.Context, c *domain.PSPClient) (int64, error) {
	if c == nil {
		return 0, errors.New("Create: nil client")
	}
	if c.ID != 0 {
		return 0, errors.New("Create: client ID must be zero")
	}
	row := pspClientToRow(c)
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// UpdateDefinition changes only the mutable upstream projection and credential
// columns of an existing stable row. UserID and PanelID scope the ID update so
// a bad caller cannot move a row across owners/panels; none of the traffic
// baselines or CreatedAt are touched.
func (r *pspClientRepo) UpdateDefinition(ctx context.Context, c *domain.PSPClient) error {
	if c == nil || c.ID == 0 {
		return errors.New("UpdateDefinition: client ID required")
	}
	return r.db.WithContext(ctx).
		Model(&pspClientRow{}).
		Where("id = ? AND user_id = ? AND panel_id = ?", c.ID, c.UserID, c.PanelID).
		Updates(map[string]any{
			"email":      c.Email,
			"cred_class": c.CredClass,
			"uuid":       c.UUID,
			"uuid_key":   domain.LineUUIDKey(c.UUID),
			"password":   c.Password,
			// A legacy row adopted as a line gains its line here; a row that
			// already has one keeps it (the planner matches by line).
			"node_id": lineNodeColumn(c),
			"line_id": lineIDColumn(c),
		}).Error
}

// DetachedClientIDs lists the rows with no psp_client_inbounds attachment.
func (r *pspClientRepo) DetachedClientIDs(ctx context.Context) (map[int64]bool, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Table("psp_clients AS c").
		Where("NOT EXISTS (SELECT 1 FROM psp_client_inbounds AS i WHERE i.client_id = c.id)").
		Pluck("c.id", &ids).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// CountAttachedClientsByPanel counts, per server, the client rows that are
// attached to at least one listener there.
func (r *pspClientRepo) CountAttachedClientsByPanel(ctx context.Context) (map[int64]int64, error) {
	var rows []struct {
		PanelID int64
		N       int64
	}
	err := r.db.WithContext(ctx).Table("psp_clients AS c").
		Select("c.panel_id AS panel_id, COUNT(*) AS n").
		Where("EXISTS (SELECT 1 FROM psp_client_inbounds AS i WHERE i.client_id = c.id)").
		Group("c.panel_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.PanelID] = row.N
	}
	return out, nil
}

// DeleteIfDetached deletes a row only while it has no attachment, in one
// statement, so a resync that re-attaches it concurrently wins.
func (r *pspClientRepo) DeleteIfDetached(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND NOT EXISTS (SELECT 1 FROM psp_client_inbounds AS i WHERE i.client_id = psp_clients.id)", id).
		Delete(&pspClientRow{}).Error
}

// LineUUIDTaken reports whether another client attached to the listener of
// nodeID holds uuidKey (domain.LineUUIDKey form).
func (r *pspClientRepo) LineUUIDTaken(ctx context.Context, nodeID int64, uuidKey string, exceptClientID int64) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Table("psp_clients AS c").
		Joins("JOIN psp_client_inbounds AS i ON i.client_id = c.id").
		Where("i.node_id = ? AND c.uuid_key = ? AND c.id <> ?", nodeID, uuidKey, exceptClientID).
		Count(&n).Error
	return n > 0, err
}

func (r *pspClientRepo) GetByID(ctx context.Context, id int64) (*domain.PSPClient, error) {
	var row pspClientRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return rowToPSPClient(&row), nil
}

func (r *pspClientRepo) GetByEmail(ctx context.Context, panelID int64, email string) (*domain.PSPClient, error) {
	var row pspClientRow
	if err := r.db.WithContext(ctx).
		Where("panel_id = ? AND email = ?", panelID, email).Order("id").
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return rowToPSPClient(&row), nil
}

func (r *pspClientRepo) ListAll(ctx context.Context) ([]*domain.PSPClient, error) {
	var rows []pspClientRow
	if err := r.db.WithContext(ctx).Order("panel_id, user_id, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.PSPClient, len(rows))
	for i := range rows {
		out[i] = rowToPSPClient(&rows[i])
	}
	return out, nil
}

func (r *pspClientRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.PSPClient, error) {
	var rows []pspClientRow
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("panel_id, id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.PSPClient, len(rows))
	for i := range rows {
		out[i] = rowToPSPClient(&rows[i])
	}
	return out, nil
}

func (r *pspClientRepo) DeleteByID(ctx context.Context, id int64) error {
	if id == 0 {
		return errors.New("DeleteByID: client ID required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row pspClientRow
		err := tx.Where("id = ?", id).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // idempotent
		}
		if err != nil {
			return err
		}
		if err := tx.Where("client_id = ?", row.ID).Delete(&pspClientInboundRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", row.ID).Delete(&pspClientRow{}).Error
	})
}

// SetInbounds reconciles the client's attachment set to the desired nodes via an
// ADDITIVE DIFF (not delete-all-recreate): rows for nodes no longer desired are
// removed, missing nodes are inserted pending, and a still-desired node's row
// is kept — preserving its convergence state/version and only updating
// FlowOverride. This is load-bearing: the shadow dual-write calls SetInbounds on
// every membership resync, and a delete-recreate would erase the observed
// convergence signal. A removed-then-readded node correctly gets fresh pending
// state and a new failure clock.
func (r *pspClientRepo) SetInbounds(ctx context.Context, clientID int64, inbounds []domain.PSPClientInbound) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []pspClientInboundRow
		if err := tx.Where("client_id = ?", clientID).Find(&existing).Error; err != nil {
			return err
		}
		curByNode := make(map[int64]pspClientInboundRow, len(existing))
		for _, e := range existing {
			curByNode[e.NodeID] = e
		}
		desiredNodes := make(map[int64]struct{}, len(inbounds))
		for _, in := range inbounds {
			desiredNodes[in.NodeID] = struct{}{}
		}

		// Remove rows whose node is no longer desired.
		for _, e := range existing {
			if _, ok := desiredNodes[e.NodeID]; !ok {
				if err := tx.Where("id = ?", e.ID).Delete(&pspClientInboundRow{}).Error; err != nil {
					return err
				}
			}
		}
		// Insert missing as pending; update flow-only on existing (state preserved).
		for _, in := range inbounds {
			cur, ok := curByNode[in.NodeID]
			if !ok {
				now := time.Now().UTC()
				if err := tx.Create(&pspClientInboundRow{
					ClientID:      clientID,
					NodeID:        in.NodeID,
					FlowOverride:  in.FlowOverride,
					State:         string(domain.ClientApplyPending),
					FirstFailedAt: &now,
				}).Error; err != nil {
					return err
				}
				continue
			}
			if cur.FlowOverride != in.FlowOverride {
				if err := tx.Model(&pspClientInboundRow{}).Where("id = ?", cur.ID).
					Update("flow_override", in.FlowOverride).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// UpdateInboundState is the only writer of attachment convergence. A missing
// FirstFailedAt starts the clock on the first pending/rejected transition and
// preserves it across retries; applied/blocked clear it.
//
// nodesync.attachmentSettled restates these rules to skip a write that would
// change nothing; a change to what is written here must be made there too.
func (r *pspClientRepo) UpdateInboundState(ctx context.Context, inbound domain.PSPClientInbound) error {
	if inbound.ClientID == 0 || inbound.NodeID == 0 {
		return errors.New("UpdateInboundState: client and node IDs required")
	}
	switch inbound.State {
	case domain.ClientApplyApplied, domain.ClientApplyPending,
		domain.ClientApplyRejected, domain.ClientApplyBlocked:
	default:
		return fmt.Errorf("UpdateInboundState: invalid state %q", inbound.State)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur pspClientInboundRow
		if err := tx.Where("client_id = ? AND node_id = ?", inbound.ClientID, inbound.NodeID).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		failedAt := inbound.FirstFailedAt
		switch inbound.State {
		case domain.ClientApplyPending, domain.ClientApplyRejected:
			if failedAt == nil {
				failedAt = cur.FirstFailedAt
			}
			if failedAt == nil {
				now := time.Now().UTC()
				failedAt = &now
			}
		default:
			failedAt = nil
		}
		updates := map[string]any{
			"state":           string(inbound.State),
			"first_failed_at": failedAt,
		}
		if inbound.State == domain.ClientApplyApplied {
			updates["applied_version"] = inbound.AppliedVersion
			if inbound.AppliedEmail != "" || inbound.AppliedUUID != "" || inbound.AppliedPassword != "" {
				updates["applied_email"] = inbound.AppliedEmail
				updates["applied_uuid"] = inbound.AppliedUUID
				updates["applied_password"] = inbound.AppliedPassword
			}
		}
		return tx.Model(&pspClientInboundRow{}).Where("id = ?", cur.ID).Updates(updates).Error
	})
}

func (r *pspClientRepo) ListInbounds(ctx context.Context, clientID int64) ([]domain.PSPClientInbound, error) {
	var rows []pspClientInboundRow
	if err := r.db.WithContext(ctx).
		Where("client_id = ?", clientID).
		Order("node_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.PSPClientInbound, len(rows))
	for i, row := range rows {
		out[i] = domain.PSPClientInbound{
			ClientID:       row.ClientID,
			NodeID:         row.NodeID,
			FlowOverride:   row.FlowOverride,
			State:          domain.ClientApplyState(row.State),
			AppliedVersion: row.AppliedVersion,
			AppliedEmail:   row.AppliedEmail, AppliedUUID: row.AppliedUUID,
			AppliedPassword: row.AppliedPassword,
			FirstFailedAt:   row.FirstFailedAt,
		}
	}
	return out, nil
}

// counterColumns is the narrow column set UpdateCounters writes — same scope as
// OwnershipRepo.UpdateCounters so the traffic poll never clobbers identity /
// credential / attachment state held by other writers.
func pspClientCounterMap(c *domain.PSPClient) map[string]any {
	return map[string]any{
		"lifetime_up_bytes":           c.LifetimeUpBytes,
		"lifetime_down_bytes":         c.LifetimeDownBytes,
		"lifetime_total_bytes":        c.LifetimeTotalBytes,
		"last_raw_up_bytes":           c.LastRawUpBytes,
		"last_raw_down_bytes":         c.LastRawDownBytes,
		"last_raw_total_bytes":        c.LastRawTotalBytes,
		"last_counter_epoch":          c.LastCounterEpoch,
		"period_baseline_up_bytes":    c.PeriodBaselineUpBytes,
		"period_baseline_down_bytes":  c.PeriodBaselineDownBytes,
		"period_baseline_total_bytes": c.PeriodBaselineTotalBytes,
		"charged_lifetime_bytes":      c.ChargedLifetimeBytes,
		"last_factor_milli":           c.LastFactor(),
	}
}

func (r *pspClientRepo) UpdateCounters(ctx context.Context, c *domain.PSPClient) error {
	if c == nil || c.ID == 0 {
		return errors.New("UpdateCounters: client ID required")
	}
	return r.db.WithContext(ctx).
		Model(&pspClientRow{}).
		Where("id = ?", c.ID).
		Updates(pspClientCounterMap(c)).Error
}

func (r *pspClientRepo) BatchUpdateCounters(ctx context.Context, items []*domain.PSPClient) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, c := range items {
			if c == nil || c.ID == 0 {
				return errors.New("BatchUpdateCounters: client ID required")
			}
			if err := tx.Model(&pspClientRow{}).
				Where("id = ?", c.ID).
				Updates(pspClientCounterMap(c)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

var (
	_ ports.PSPClientRepo   = (*pspClientRepo)(nil)
	_ ports.LineUUIDChecker = (*pspClientRepo)(nil)
)
