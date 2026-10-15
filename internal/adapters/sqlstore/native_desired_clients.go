package sqlstore

import (
	"database/sql"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Read every client field in the caller's snapshot transaction. Fixed scan
// destinations avoid GORM's per-field reflection and the intermediate complete
// row slice; NULL historic counters retain the scalar model's zero semantics.
func loadNativeDesiredClients(query *gorm.DB, email *string) ([]ports.NativeDesiredClient, error) {
	rows, err := query.Model(&pspClientRow{}).Select(
		"id", "user_id", "panel_id", "email", "cred_class", "uuid", "password",
		"desired_enable", "desired_expiry_time", "panel_quota_headroom", "panel_ip_limit", "panel_device_limit", "desired_minted", "created_at",
		"lifetime_up_bytes", "lifetime_down_bytes", "lifetime_total_bytes",
		"last_raw_up_bytes", "last_raw_down_bytes", "last_raw_total_bytes", "last_counter_epoch",
		"period_baseline_up_bytes", "period_baseline_down_bytes", "period_baseline_total_bytes",
	).Order("id").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var row pspClientRow
	var created sql.NullTime
	var lifetimeUp, lifetimeDown, lifetimeTotal, rawUp, rawDown, rawTotal, baselineUp, baselineDown, baselineTotal sql.NullInt64
	var epoch sql.Null[uint64]
	// Keep the original nonnil empty client collection.
	out := make([]ports.NativeDesiredClient, 0)
	for rows.Next() {
		if err := rows.Scan(
			&row.ID, &row.UserID, &row.PanelID, &row.Email, &row.CredClass, &row.UUID, &row.Password,
			&row.DesiredEnable, &row.DesiredExpiryTime, &row.PanelQuotaHeadroom, &row.PanelIPLimit, &row.PanelDeviceLimit, &row.DesiredMinted, &created,
			&lifetimeUp, &lifetimeDown, &lifetimeTotal, &rawUp, &rawDown, &rawTotal, &epoch,
			&baselineUp, &baselineDown, &baselineTotal,
		); err != nil {
			return nil, err
		}
		// MySQL's case/accent-insensitive collation may return more than one
		// spelling. Exact byte identity still decides credential lookup.
		if email != nil && row.Email != *email {
			continue
		}
		row.CreatedAt = created.Time
		row.LifetimeUpBytes, row.LifetimeDownBytes, row.LifetimeTotalBytes = lifetimeUp.Int64, lifetimeDown.Int64, lifetimeTotal.Int64
		row.LastRawUpBytes, row.LastRawDownBytes, row.LastRawTotalBytes = rawUp.Int64, rawDown.Int64, rawTotal.Int64
		row.LastCounterEpoch = epoch.V
		row.PeriodBaselineUpBytes, row.PeriodBaselineDownBytes, row.PeriodBaselineTotalBytes = baselineUp.Int64, baselineDown.Int64, baselineTotal.Int64
		out = append(out, ports.NativeDesiredClient{Client: rowToPSPClient(&row)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
