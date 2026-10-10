package destaudit

import (
	"cmp"
	"slices"
	"strings"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type mappedBatch struct {
	hits   []domain.DestHit
	usage  []domain.DestUsage
	losses map[string]int64
}

func mapBatch(panelID int64, body protocol.AuditObservation, users map[int64]bool, now time.Time) mappedBatch {
	out := mappedBatch{losses: map[string]int64{}}
	// Protocol validation has already made every row share the batch hour.
	// Use the receiver clock; the peer's control ReportedAtMS is irrelevant.
	if body.Hour < now.Add(-48*time.Hour).UnixMilli() || body.Hour > now.Add(time.Hour).UnixMilli() {
		if rows := len(body.Hits) + len(body.Usage); rows > 0 {
			out.losses["out_of_range"] = int64(rows)
		}
		return out
	}
	type hitKey struct {
		hour, user           int64
		source, action, dest string
		port                 uint16
	}
	hits := make(map[hitKey]domain.DestHit, len(body.Hits))
	for _, hit := range body.Hits {
		userID := int64(0)
		if body.Kind != "trial" {
			var err error
			userID, err = hit.Subject.RowID()
			if err != nil || !users[userID] {
				out.losses["unknown_subject"]++
				continue
			}
		}
		// This only removes a validated fragment suffix. A source need not
		// still exist; its frozen action is never replaced with a current one.
		source := hit.RuleID
		if at := strings.IndexByte(source, 'x'); at >= 0 {
			source = source[:at]
		}
		key := hitKey{hit.Hour, userID, source, hit.Action, hit.Dest, hit.Port}
		first, last := time.UnixMilli(hit.FirstMS).UTC(), time.UnixMilli(hit.LastMS).UTC()
		row, found := hits[key]
		if !found {
			row = domain.DestHit{HourMS: hit.Hour, PanelID: panelID, UserID: userID, Source: source, Action: hit.Action, Dest: hit.Dest, Port: int(hit.Port), FirstAt: first, LastAt: last}
		} else {
			if first.Before(row.FirstAt) {
				row.FirstAt = first
			}
			if last.After(row.LastAt) {
				row.LastAt = last
			}
		}
		// At most MaxAuditHits uint32 counts are merged: the sum fits int64.
		row.Count += int64(hit.Count)
		hits[key] = row
	}
	for _, row := range hits {
		out.hits = append(out.hits, row)
	}
	// Deterministic final-key order also defines over-budget truncation.
	slices.SortFunc(out.hits, func(a, b domain.DestHit) int {
		return cmp.Or(cmp.Compare(a.HourMS, b.HourMS), cmp.Compare(a.UserID, b.UserID), cmp.Compare(a.Source, b.Source), cmp.Compare(a.Action, b.Action), cmp.Compare(a.Dest, b.Dest), cmp.Compare(a.Port, b.Port))
	})
	type usageKey struct {
		hour, user int64
		site       string
	}
	usage := make(map[usageKey]domain.DestUsage, len(body.Usage))
	for _, hit := range body.Usage {
		userID, err := hit.Subject.RowID()
		if err != nil || !users[userID] {
			out.losses["unknown_subject"]++
			continue
		}
		key := usageKey{hit.Hour, userID, hit.Site}
		row := usage[key]
		row.HourMS, row.PanelID, row.UserID, row.Site = hit.Hour, panelID, userID, hit.Site
		row.Count += int64(hit.Count)
		usage[key] = row
	}
	for _, row := range usage {
		out.usage = append(out.usage, row)
	}
	slices.SortFunc(out.usage, func(a, b domain.DestUsage) int {
		return cmp.Or(cmp.Compare(a.HourMS, b.HourMS), cmp.Compare(a.UserID, b.UserID), cmp.Compare(a.Site, b.Site))
	})
	return out
}
