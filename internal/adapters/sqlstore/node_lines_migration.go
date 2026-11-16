package sqlstore

import (
	"encoding/json"
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

// backfillLineLabels gives relay and VLESS route lines written before ADR 0040
// the node's Region and Tags. It decides per line by key presence, not by a
// completion marker: a row an older binary rewrote after a downgrade loses the
// keys again and is completed on the next start, while a key present with an
// empty value is an explicit clear and is left alone. Copying the node's
// values verbatim keeps every rendered subscription byte-identical. Each
// column is written with compare-and-swap against the exact text read, so a
// concurrently starting replica makes this start fail and retry.
func backfillLineLabels(db *gorm.DB) error {
	var rows []struct {
		ID         int64
		Region     string
		Tags       jsonStrings
		Relays     *string
		RouteState *string
	}
	if err := db.Table("nodes").Select("id, region, tags, relays, route_state").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.Relays != nil {
			if relays, changed, err := relaysWithLabels(*row.Relays, row.Region, row.Tags); err != nil {
				return fmt.Errorf("line labels for node %d: %w", row.ID, err)
			} else if changed {
				if err := casNodeColumn(db, row.ID, "relays", *row.Relays, relays); err != nil {
					return err
				}
			}
		}
		if row.RouteState != nil {
			if state, changed, err := routeStateWithLabels(*row.RouteState, row.Region, row.Tags); err != nil {
				return fmt.Errorf("line labels for node %d: %w", row.ID, err)
			} else if changed {
				if err := casNodeColumn(db, row.ID, "route_state", *row.RouteState, state); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func casNodeColumn(db *gorm.DB, nodeID int64, column, previous string, next any) error {
	result := db.Table("nodes").Where("id = ? AND "+column+" = ?", nodeID, previous).Update(column, next)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("line labels for node %d changed concurrently; retry startup", nodeID)
	}
	return nil
}

// missingLabelKeys reports, per line, whether the stored JSON lacks the
// region/tags keys (under the given key spellings).
func missingLabelKeys(raw []byte, regionKey, tagsKey string) ([][2]bool, error) {
	var lines []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	out := make([][2]bool, len(lines))
	for i, line := range lines {
		_, hasRegion := line[regionKey]
		_, hasTags := line[tagsKey]
		out[i] = [2]bool{!hasRegion, !hasTags}
	}
	return out, nil
}

func relaysWithLabels(raw, region string, tags []string) (jsonRelays, bool, error) {
	if raw == "" || raw == "[]" {
		return nil, false, nil
	}
	missing, err := missingLabelKeys([]byte(raw), "Region", "Tags")
	if err != nil {
		return nil, false, err
	}
	var lines jsonRelays
	if err := lines.Scan(raw); err != nil {
		return nil, false, err
	}
	if len(lines) != len(missing) {
		return nil, false, fmt.Errorf("relay line count mismatch")
	}
	changed := false
	for i := range lines {
		if missing[i][0] {
			lines[i].Region, changed = region, true
		}
		if missing[i][1] {
			lines[i].Tags, changed = domain.CopyLineTags(tags), true
		}
	}
	return lines, changed, nil
}

func routeStateWithLabels(raw, region string, tags []string) (jsonRouteState, bool, error) {
	if raw == "" {
		return jsonRouteState{}, false, nil
	}
	var envelope struct {
		Lines json.RawMessage `json:"lines"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return jsonRouteState{}, false, err
	}
	if len(envelope.Lines) == 0 || string(envelope.Lines) == "null" {
		return jsonRouteState{}, false, nil
	}
	missing, err := missingLabelKeys(envelope.Lines, "region", "tags")
	if err != nil {
		return jsonRouteState{}, false, err
	}
	var state jsonRouteState
	if err := state.Scan(raw); err != nil {
		return jsonRouteState{}, false, err
	}
	if len(state.Lines) != len(missing) {
		return jsonRouteState{}, false, fmt.Errorf("route line count mismatch")
	}
	changed := false
	for i := range state.Lines {
		if missing[i][0] {
			state.Lines[i].Region, changed = region, true
		}
		if missing[i][1] {
			state.Lines[i].Tags, changed = domain.CopyLineTags(tags), true
		}
	}
	return state, changed, nil
}
