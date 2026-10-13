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
				current[inboundStateKey{rows[i].ClientID, rows[i].NodeID}] = &rows[i]
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
			updates := map[string]any{"state": string(inbound.State), "first_failed_at": failedAt}
			if inbound.State == domain.ClientApplyApplied {
				version = inbound.AppliedVersion
				updates["applied_version"] = version
				if inbound.AppliedEmail != "" || inbound.AppliedUUID != "" || inbound.AppliedPassword != "" {
					email, uuid, password = inbound.AppliedEmail, inbound.AppliedUUID, inbound.AppliedPassword
					updates["applied_email"], updates["applied_uuid"], updates["applied_password"] = email, uuid, password
				}
			}
			sameClock := row.FirstFailedAt == nil && failedAt == nil || row.FirstFailedAt != nil && failedAt != nil && row.FirstFailedAt.Equal(*failedAt)
			if row.State == string(inbound.State) && sameClock && row.AppliedVersion == version && row.AppliedEmail == email && row.AppliedUUID == uuid && row.AppliedPassword == password {
				continue
			}
			if err := tx.Model(&pspClientInboundRow{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return err
			}
			row.State, row.FirstFailedAt, row.AppliedVersion = string(inbound.State), failedAt, version
			row.AppliedEmail, row.AppliedUUID, row.AppliedPassword = email, uuid, password
		}
		return nil
	})
}

var _ ports.PSPClientInboundBatchRepo = (*pspClientRepo)(nil)
