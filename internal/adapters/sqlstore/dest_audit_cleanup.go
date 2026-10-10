package sqlstore

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

// Retention and orphan deletes commit together. No diagnostic count can claim
// a delete that was rolled back, and the audit SQL boundary stays private.
func (r *DestAuditRepo) PruneDestinationAudit(ctx context.Context, now time.Time, settings *domain.DestinationSettings) (domain.DestAuditPruned, error) {
	if now.IsZero() {
		return domain.DestAuditPruned{}, domain.ErrValidation
	}
	var removed domain.DestAuditPruned
	err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		deleteRows := func(model any, where string, args []any, count *int64) error {
			result := tx.Where(where, args...).Delete(model)
			if result.Error != nil {
				return result.Error
			}
			*count += result.RowsAffected
			return nil
		}
		trial := "action = 'observe' AND " + auditExactGroupSource(tx.Dialector.Name())
		if settings != nil {
			effective := settings.Effective()
			hour := now.UTC().Truncate(time.Hour)
			hitCut := hour.Add(-time.Duration(effective.HitRetentionDays) * 24 * time.Hour).UnixMilli()
			trialCut := hour.Add(-time.Duration(effective.TrialRetentionDays) * 24 * time.Hour).UnixMilli()
			usageCut := hour.Add(-time.Duration(effective.UsageRetentionDays) * 24 * time.Hour).UnixMilli()
			if err := deleteRows(&destHitRow{}, "hour_ms < ? AND NOT ("+trial+")", []any{hitCut}, &removed.Hits); err != nil {
				return err
			}
			if err := deleteRows(&destHitRow{}, "hour_ms < ? AND ("+trial+")", []any{trialCut}, &removed.Trial); err != nil {
				return err
			}
			if err := deleteRows(&destUsageHourlyRow{}, "hour_ms < ?", []any{usageCut}, &removed.Usage); err != nil {
				return err
			}
			if err := deleteRows(&destAuditLossHourlyRow{}, "(kind IN ('block', 'observe') AND observed_hour_ms < ?) OR (kind = 'trial' AND observed_hour_ms < ?) OR (kind = 'usage' AND observed_hour_ms < ?)", []any{hitCut, trialCut, usageCut}, &removed.Loss); err != nil {
				return err
			}
		}
		cut := now.UTC().Add(-72 * time.Hour)
		if err := deleteRows(&destAuditBatchRow{}, "received_at < ?", []any{cut}, &removed.Batches); err != nil {
			return err
		}
		// A budget bucket spans a whole hour. Keeping the boundary hour avoids
		// deleting it less than 72h after its last possible accepted row.
		if err := deleteRows(&destAuditIngestBudgetRow{}, "received_hour_ms < ?", []any{cut.Truncate(time.Hour).UnixMilli()}, &removed.Budget); err != nil {
			return err
		}
		groupSource := "'g' || CAST(groups_.id AS TEXT)"
		groupMatch := "dest_hits.source = " + groupSource
		if tx.Dialector.Name() == "mysql" {
			groupSource = "CONCAT('g', CAST(groups_.id AS CHAR))"
			// MySQL may use a case-insensitive, space-padding collation. An
			// anonymous trial source must match the canonical bytes exactly.
			groupMatch = "BINARY dest_hits.source = BINARY " + groupSource
		}
		validTrial := "user_id = 0 AND port = 0 AND action = 'observe' AND EXISTS (SELECT 1 FROM groups_ WHERE " + groupMatch + ")"
		deletes := []struct {
			model any
			where string
		}{
			{&destHitRow{}, "NOT EXISTS (SELECT 1 FROM xui_panels WHERE xui_panels.id = dest_hits.panel_id) OR user_id < 0 OR (user_id > 0 AND NOT EXISTS (SELECT 1 FROM users WHERE users.id = dest_hits.user_id)) OR (user_id = 0 AND NOT (" + validTrial + "))"},
			{&destUsageHourlyRow{}, "user_id <= 0 OR NOT EXISTS (SELECT 1 FROM users WHERE users.id = dest_usage_hourly.user_id) OR NOT EXISTS (SELECT 1 FROM xui_panels WHERE xui_panels.id = dest_usage_hourly.panel_id)"},
			{&destAgentPolicyRow{}, "NOT EXISTS (SELECT 1 FROM node_agents WHERE node_agents.agent_id = dest_agent_policy.agent_id)"},
			{&destAuditBatchRow{}, "NOT (agent_id = '' AND kind = 'receiver_loss') AND NOT EXISTS (SELECT 1 FROM node_agents WHERE node_agents.agent_id = dest_audit_batches.agent_id)"},
			{&destAuditIngestBudgetRow{}, "NOT EXISTS (SELECT 1 FROM node_agents WHERE node_agents.agent_id = dest_audit_ingest_budget.agent_id)"},
			{&destAuditLossHourlyRow{}, "NOT EXISTS (SELECT 1 FROM xui_panels WHERE xui_panels.id = dest_audit_loss_hourly.panel_id)"},
		}
		for _, entry := range deletes {
			if err := deleteRows(entry.model, entry.where, nil, &removed.Orphans); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.DestAuditPruned{}, auditStorageError(err)
	}
	return removed, nil
}

func auditExactGroupSource(dialect string) string {
	switch dialect {
	case "mysql":
		return "source REGEXP '^g[1-9][0-9]*$' AND source NOT REGEXP '[^g0-9]' AND ASCII(LEFT(source, 1)) = 103"
	case "postgres":
		return "source ~ '^g[1-9][0-9]*$' AND source !~ '[^g0-9]'"
	default:
		return "source GLOB 'g[1-9]*' AND substr(source, 2) NOT GLOB '*[^0-9]*'"
	}
}
