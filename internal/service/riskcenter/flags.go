package riskcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// FlagPage is one page of flag_records and the names of the admins its
// review records name. Actors maps an admin's id to the admin's CURRENT UPN;
// an admin deleted since has no entry (the SPA shows the id).
type FlagPage struct {
	Records []domain.FlagRecord
	Total   int64
	Actors  map[int64]string
}

// ReviewActor is the admin a review record names (its params' by), and
// false for any other record or params that do not parse. The record stores
// the id only — flag_records is name-free — so every reader that shows the
// admin resolves it with this and a user read.
func ReviewActor(r domain.FlagRecord) (int64, bool) {
	if r.Source != domain.FlagSourceReview || len(r.Params) == 0 {
		return 0, false
	}
	var p domain.ReviewFlagParams
	if err := json.Unmarshal(r.Params, &p); err != nil || p.By <= 0 {
		return 0, false
	}
	return p.By, true
}

// Flags is one page of flag_records, as the store filters and pages it
// (newest first), with the admins its review records name, read in one
// lookup and only when the page has a review record. The filter is the
// store's to validate.
//
// The names are resolved here, at read time, rather than stored: a renamed
// admin reads under the current name, and the history holds no name to
// keep for months.
func (s *Service) Flags(ctx context.Context, f ports.FlagRecordFilter) (FlagPage, error) {
	recs, total, err := s.d.Flags.List(ctx, f)
	if err != nil {
		return FlagPage{}, err
	}
	pg := FlagPage{Records: recs, Total: total, Actors: map[int64]string{}}
	var ids []int64
	for _, r := range recs {
		if id, ok := ReviewActor(r); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return pg, nil
	}
	slices.Sort(ids)
	admins, err := s.d.Users.ListByIDs(ctx, ids)
	if err != nil {
		return FlagPage{}, fmt.Errorf("risk center: name review actors: %w", err)
	}
	for _, u := range admins {
		if u != nil {
			pg.Actors[u.ID] = u.UPN
		}
	}
	return pg, nil
}
