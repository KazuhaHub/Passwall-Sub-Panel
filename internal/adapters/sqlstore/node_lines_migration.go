package sqlstore

import (
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

// backfillRelayLineIDs is idempotent and only changes the relay JSON column.
// The compare-and-swap also prevents concurrent starting replicas from
// publishing different IDs for the same historical entry.
func backfillRelayLineIDs(db *gorm.DB) error {
	var rows []struct {
		ID     int64
		Relays string
	}
	if err := db.Table("nodes").Select("id, relays").Where("relays IS NOT NULL AND relays <> '' AND relays <> '[]'").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var lines jsonRelays
		if err := lines.Scan(row.Relays); err != nil {
			return fmt.Errorf("relay IDs for node %d: %w", row.ID, err)
		}
		changed := domain.AssignRelayIDs([]domain.RelayLine(lines))
		if err := domain.ValidateRelayIDs([]domain.RelayLine(lines)); err != nil {
			return fmt.Errorf("relay IDs for node %d: %w", row.ID, err)
		}
		if !changed {
			continue
		}
		result := db.Table("nodes").Where("id = ? AND relays = ?", row.ID, row.Relays).Update("relays", lines)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("relay IDs for node %d changed concurrently; retry startup", row.ID)
		}
	}
	return nil
}
