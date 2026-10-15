package sqlstore

import (
	"database/sql"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Keep every current model field, including the JSON scanners whose errors
// must propagate even when their values do not enter domain.User. COALESCE
// preserves GORM's zero-on-NULL behavior for scalar fields; nullable override
// pointers retain the separate inherited/explicit-zero/finite states.
const currentUserColumns = `id,upn,sso_provider,sso_subject,
COALESCE(email,'') AS email,COALESCE(password_hash,'') AS password_hash,
role,sub_token,uuid,group_id,enabled_rule_sets,COALESCE(personal_rules,'') AS personal_rules,
expire_at,traffic_limit_bytes,ip_limit,device_limit,COALESCE(traffic_reset_period,'') AS traffic_reset_period,
traffic_period_start,COALESCE(lifetime_up_bytes,0) AS lifetime_up_bytes,
COALESCE(lifetime_down_bytes,0) AS lifetime_down_bytes,COALESCE(lifetime_total_bytes,0) AS lifetime_total_bytes,
COALESCE(period_baseline_bytes,0) AS period_baseline_bytes,lifetime_baseline_at,
COALESCE(display_name,'') AS display_name,COALESCE(remark,'') AS remark,enabled,
COALESCE(auto_disabled_reason,'') AS auto_disabled_reason,COALESCE(disable_detail,'') AS disable_detail,
service_disabled_reason,COALESCE(service_disable_detail,'') AS service_disable_detail,service_disabled_at,
self_registered,COALESCE(block_violation_count,0) AS block_violation_count,last_block_violation_at,
COALESCE(emergency_used_count,0) AS emergency_used_count,emergency_until,
COALESCE(emergency_baseline_bytes,0) AS emergency_baseline_bytes,token_version,
totp_secret,totp_enabled,recovery_codes,permission_overrides,last_online_at,created_at,updated_at`

func loadCurrentUserRows(query *gorm.DB) ([]*domain.User, error) {
	rows, err := query.Model(&userRow{}).Select(currentUserColumns).Order("id").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*domain.User
	for rows.Next() {
		// Fresh ownership matters: returned override/time pointers and decoded
		// JSON collections must never alias the next row's scan destinations.
		var row userRow
		var created, updated sql.NullTime
		if err := rows.Scan(
			&row.ID, &row.UPN, &row.SSOProvider, &row.SSOSubject, &row.Email, &row.PasswordHash,
			&row.Role, &row.SubToken, &row.UUID, &row.GroupID, &row.EnabledRuleSets, &row.PersonalRules,
			&row.ExpireAt, &row.TrafficLimitBytes, &row.IPLimit, &row.DeviceLimit, &row.TrafficResetPeriod,
			&row.TrafficPeriodStart, &row.LifetimeUpBytes, &row.LifetimeDownBytes, &row.LifetimeTotalBytes,
			&row.PeriodBaselineBytes, &row.LifetimeBaselineAt, &row.DisplayName, &row.Remark, &row.Enabled,
			&row.AutoDisabledReason, &row.DisableDetail, &row.ServiceDisabledReason, &row.ServiceDisableDetail, &row.ServiceDisabledAt,
			&row.SelfRegistered, &row.BlockViolationCount, &row.LastBlockViolationAt, &row.EmergencyUsedCount,
			&row.EmergencyUntil, &row.EmergencyBaselineBytes, &row.TokenVersion, &row.TOTPSecret, &row.TOTPEnabled,
			&row.RecoveryCodes, &row.PermissionOverrides, &row.LastOnlineAt, &created, &updated,
		); err != nil {
			return nil, err
		}
		row.CreatedAt, row.UpdatedAt = created.Time, updated.Time
		users = append(users, row.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return users, nil
}
