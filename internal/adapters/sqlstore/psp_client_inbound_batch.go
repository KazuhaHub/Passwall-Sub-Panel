package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type inboundStateKey struct{ client, node int64 }

func (r *pspClientRepo) UpdateInboundStates(ctx context.Context, inbounds []domain.PSPClientInbound) error {
	if len(inbounds) == 0 {
		return nil
	}
	keys := make(map[inboundStateKey]bool, len(inbounds))
	for _, inbound := range inbounds {
		if inbound.ClientID == 0 || inbound.NodeID == 0 {
			return errors.New("UpdateInboundStates: client and node IDs required")
		}
		switch inbound.State {
		case domain.ClientApplyApplied, domain.ClientApplyPending, domain.ClientApplyRejected, domain.ClientApplyBlocked:
		default:
			return fmt.Errorf("UpdateInboundStates: invalid state %q", inbound.State)
		}
		keys[inboundStateKey{inbound.ClientID, inbound.NodeID}] = true
	}
	pairs := make([]inboundStateKey, 0, len(keys))
	for pair := range keys {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].client < pairs[j].client || pairs[i].client == pairs[j].client && pairs[i].node < pairs[j].node
	})
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current := make(map[inboundStateKey]*pspClientInboundRow, len(pairs))
		original := make(map[inboundStateKey]pspClientInboundRow, len(pairs))
		// Two bound parameters per identity; every read remains within 500.
		for start := 0; start < len(pairs); start += 250 {
			end := min(start+250, len(pairs))
			terms, args := make([]string, 0, end-start), make([]any, 0, 2*(end-start))
			for _, pair := range pairs[start:end] {
				terms = append(terms, "(client_id = ? AND node_id = ?)")
				args = append(args, pair.client, pair.node)
			}
			var rows []pspClientInboundRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(strings.Join(terms, " OR "), args...).Order("client_id, node_id").Find(&rows).Error; err != nil {
				return err
			}
			for i := range rows {
				key := inboundStateKey{rows[i].ClientID, rows[i].NodeID}
				current[key], original[key] = &rows[i], rows[i]
			}
		}
		// Preserve input order when the same attachment occurs more than once.
		for _, inbound := range inbounds {
			row := current[inboundStateKey{inbound.ClientID, inbound.NodeID}]
			if row == nil {
				continue
			}
			failedAt := inbound.FirstFailedAt
			if inbound.State == domain.ClientApplyPending || inbound.State == domain.ClientApplyRejected {
				if failedAt == nil {
					failedAt = row.FirstFailedAt
				}
				if failedAt == nil {
					now := time.Now().UTC()
					failedAt = &now
				}
			} else {
				failedAt = nil
			}
			version, email, uuid, password := row.AppliedVersion, row.AppliedEmail, row.AppliedUUID, row.AppliedPassword
			if inbound.State == domain.ClientApplyApplied {
				version = inbound.AppliedVersion
				if inbound.AppliedEmail != "" || inbound.AppliedUUID != "" || inbound.AppliedPassword != "" {
					email, uuid, password = inbound.AppliedEmail, inbound.AppliedUUID, inbound.AppliedPassword
				}
			}
			row.State, row.FirstFailedAt, row.AppliedVersion = string(inbound.State), failedAt, version
			row.AppliedEmail, row.AppliedUUID, row.AppliedPassword = email, uuid, password
		}
		var changed []*pspClientInboundRow
		for _, key := range pairs {
			row := current[key]
			if row != nil && !sameInboundObservedState(*row, original[key]) {
				changed = append(changed, row)
			}
		}
		// Six CASE columns bind (id, value), plus one WHERE id per row:
		// 32 rows use 416 parameters. Keep all chunks in this same transaction.
		for start := 0; start < len(changed); start += 32 {
			if err := updateInboundObservedRows(tx, changed[start:min(start+32, len(changed))], original); err != nil {
				return err
			}
		}
		return nil
	})
}

func sameInboundObservedState(a, b pspClientInboundRow) bool {
	return a.State == b.State && sameInboundFailureClock(a, b) && a.AppliedVersion == b.AppliedVersion && a.AppliedEmail == b.AppliedEmail && a.AppliedUUID == b.AppliedUUID && a.AppliedPassword == b.AppliedPassword
}

func sameInboundFailureClock(a, b pspClientInboundRow) bool {
	return a.FirstFailedAt == nil && b.FirstFailedAt == nil || a.FirstFailedAt != nil && b.FirstFailedAt != nil && a.FirstFailedAt.Equal(*b.FirstFailedAt)
}

func updateInboundObservedRows(tx *gorm.DB, rows []*pspClientInboundRow, original map[inboundStateKey]pspClientInboundRow) error {
	columns := []struct {
		name    string
		value   func(*pspClientInboundRow) any
		changed func(pspClientInboundRow, pspClientInboundRow) bool
	}{
		{"state", func(r *pspClientInboundRow) any { return r.State }, func(a, b pspClientInboundRow) bool { return a.State != b.State }},
		{"first_failed_at", func(r *pspClientInboundRow) any { return r.FirstFailedAt }, func(a, b pspClientInboundRow) bool { return !sameInboundFailureClock(a, b) }},
		{"applied_version", func(r *pspClientInboundRow) any { return r.AppliedVersion }, func(a, b pspClientInboundRow) bool { return a.AppliedVersion != b.AppliedVersion }},
		{"applied_email", func(r *pspClientInboundRow) any { return r.AppliedEmail }, func(a, b pspClientInboundRow) bool { return a.AppliedEmail != b.AppliedEmail }},
		{"applied_uuid", func(r *pspClientInboundRow) any { return r.AppliedUUID }, func(a, b pspClientInboundRow) bool { return a.AppliedUUID != b.AppliedUUID }},
		{"applied_password", func(r *pspClientInboundRow) any { return r.AppliedPassword }, func(a, b pspClientInboundRow) bool { return a.AppliedPassword != b.AppliedPassword }},
	}
	updates := make(map[string]any, len(columns))
	for _, column := range columns {
		var expression strings.Builder
		expression.WriteString("CASE id")
		args := make([]any, 0, 2*len(rows))
		for _, row := range rows {
			if !column.changed(*row, original[inboundStateKey{row.ClientID, row.NodeID}]) {
				continue
			}
			expression.WriteString(" WHEN ? THEN ?")
			args = append(args, row.ID, column.value(row))
		}
		if len(args) == 0 {
			continue
		}
		expression.WriteString(" ELSE " + column.name + " END")
		updates[column.name] = gorm.Expr(expression.String(), args...)
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return tx.Model(&pspClientInboundRow{}).Where("id IN ?", ids).Updates(updates).Error
}

var _ ports.PSPClientInboundBatchRepo = (*pspClientRepo)(nil)
