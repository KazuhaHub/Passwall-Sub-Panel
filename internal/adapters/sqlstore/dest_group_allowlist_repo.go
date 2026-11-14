package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A group's allowlist is stored on its groups_ row (see groupRow). NULL reads
// as off.

func (r *groupRow) destAllowlist() domain.DestAllowlist {
	a := domain.DestAllowlist{State: domain.DestStateOff, StateChangedAt: utcPtr(r.DestAllowlistStateChangedAt), UpdatedAt: utcPtr(r.DestAllowlistUpdatedAt)}
	if r.DestAllowlistState != nil {
		a.State = domain.DestState(*r.DestAllowlistState)
	}
	if r.DestAllowlistListIDs != nil {
		_ = json.Unmarshal([]byte(*r.DestAllowlistListIDs), &a.ListIDs)
		if len(a.ListIDs) == 0 {
			a.ListIDs = nil
		}
	}
	if r.DestAllowlistBaseListID != nil {
		a.BaseListID = *r.DestAllowlistBaseListID
	}
	if r.DestAllowlistExtraListID != nil {
		a.ExtraListID = *r.DestAllowlistExtraListID
	}
	return a
}

// setDestAllowlist writes a into the row's columns; the zero value (no state)
// is all NULL.
func (r *groupRow) setDestAllowlist(a domain.DestAllowlist) {
	r.DestAllowlistState, r.DestAllowlistListIDs, r.DestAllowlistBaseListID, r.DestAllowlistExtraListID = nil, nil, nil, nil
	r.DestAllowlistStateChangedAt, r.DestAllowlistUpdatedAt = utcPtr(a.StateChangedAt), utcPtr(a.UpdatedAt)
	if a.State == "" {
		return
	}
	st := string(a.State)
	ids, _ := json.Marshal(sortedUniqueIDs(a.ListIDs))
	lists, base, extra := string(ids), a.BaseListID, a.ExtraListID
	r.DestAllowlistState, r.DestAllowlistListIDs, r.DestAllowlistBaseListID, r.DestAllowlistExtraListID = &st, &lists, &base, &extra
}

func sortedUniqueIDs(in []int64) []int64 {
	out := slices.Clone(in)
	if out == nil {
		out = []int64{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func validateDestAllowlist(a *domain.DestAllowlist) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: group allowlist: "+format, append([]any{domain.ErrValidation}, args...)...)
	}
	switch {
	case !a.State.Valid():
		return fail("unknown state %q", a.State)
	case a.BaseListID < 0 || a.ExtraListID < 0:
		return fail("list ids must not be negative")
	}
	for _, id := range a.ListIDs {
		if id <= 0 {
			return fail("list id %d is not a list", id)
		}
	}
	return nil
}

// DestGroupAllowlistRepo writes the allowlist columns of groups_ from the
// destination side: only those columns, under the destination state lock, and
// the generation advances in the same transaction.
type DestGroupAllowlistRepo struct{ db *gorm.DB }

// Get reads a group's allowlist; domain.ErrNotFound when there is no such group.
func (r *DestGroupAllowlistRepo) Get(ctx context.Context, groupID int64) (*domain.DestAllowlist, error) {
	var row groupRow
	res := r.db.WithContext(ctx).Where("id = ?", groupID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("get group allowlist: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	a := row.destAllowlist()
	return &a, nil
}

// saveAllowlistTx writes the allowlist of group gid on tx (the destination
// state is already locked). ifUpdatedAt, when given, must equal the stored
// updated_at (nil there never matches). The state's time moves only with the
// state, the updated_at on every write. a is given the stored times.
func saveAllowlistTx(tx *gorm.DB, gid int64, a *domain.DestAllowlist, ifUpdatedAt *time.Time) error {
	var cur groupRow
	res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", gid).Limit(1).Find(&cur)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	if err := requireAllowlistListsTx(tx, a); err != nil {
		return err
	}
	prev := cur.destAllowlist()
	if ifUpdatedAt != nil && (prev.UpdatedAt == nil || prev.UpdatedAt.UnixMilli() != ifUpdatedAt.UnixMilli()) {
		return fmt.Errorf("%w: the group's allowlist changed since it was read", domain.ErrConflict)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	next := *a
	next.UpdatedAt, next.StateChangedAt = &now, &now
	if prev.UpdatedAt != nil && !now.After(*prev.UpdatedAt) {
		t := prev.UpdatedAt.Add(time.Millisecond)
		next.UpdatedAt = &t
	}
	if prev.StateChangedAt != nil && prev.State == next.State && cur.DestAllowlistState != nil {
		next.StateChangedAt = prev.StateChangedAt
	}
	var row groupRow
	row.setDestAllowlist(next)
	if err := tx.Model(&groupRow{}).Where("id = ?", gid).Select(groupAllowlistColumns).Updates(&row).Error; err != nil {
		return err
	}
	a.StateChangedAt, a.UpdatedAt = next.StateChangedAt, next.UpdatedAt
	a.ListIDs = sortedUniqueIDs(a.ListIDs)
	return bumpDestGeneration(tx, time.Now())
}

// requireAllowlistListsTx checks, inside the locked transaction, the lists an
// allowlist names: they exist (a list deleted between the admin's read and this
// write would leave the group pointing at nothing, and an allowlist built on
// nothing denies everything), and an allowlist that is on has them all ready.
// DeleteUnused takes the same state lock, so no delete lands between this check
// and the write.
func requireAllowlistListsTx(tx *gorm.DB, a *domain.DestAllowlist) error {
	ids := sortedUniqueIDs(a.ListIDs)
	if len(ids) == 0 {
		return nil
	}
	var rows []destListRow
	if err := tx.Select("id", "kind", "last_fetched_at", "entry_count").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return fmt.Errorf("%w: group allowlist: a list it names does not exist", domain.ErrValidation)
	}
	if a.State != domain.DestStateOn {
		return nil
	}
	var notReady []int64
	for _, row := range rows {
		if !row.toDomain().Ready() {
			notReady = append(notReady, row.ID)
		}
	}
	if len(notReady) > 0 {
		slices.Sort(notReady)
		return &domain.DestListNotReadyError{ListIDs: notReady}
	}
	return nil
}

// Save writes group gid's allowlist and advances the generation.
// domain.ErrNotFound: no such group; domain.ErrConflict: ifUpdatedAt is stale.
func (r *DestGroupAllowlistRepo) Save(ctx context.Context, gid int64, a *domain.DestAllowlist, ifUpdatedAt *time.Time) error {
	if err := validateDestAllowlist(a); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		return saveAllowlistTx(tx, gid, a, ifUpdatedAt)
	})
}

// CreateWithOwnedLists gives a group its first allowlist together with the two
// lists it owns (the base allowance and the extra allowances), in ONE
// transaction: no failure leaves a list without its group or an allowlist
// pointing at a list that is not there, and the generation advances once. The
// lists arrive built (the canonical entries are the list service's to
// produce); this sets their owner, and the new ids on them and on a. A group
// that already has an allowlist is domain.ErrAlreadyExists.
func (r *DestGroupAllowlistRepo) CreateWithOwnedLists(ctx context.Context, gid int64, a *domain.DestAllowlist, base, extra *domain.DestList) error {
	if err := validateDestAllowlist(a); err != nil {
		return err
	}
	for _, l := range []*domain.DestList{base, extra} {
		if l == nil {
			return fmt.Errorf("%w: group allowlist: both owned lists are needed", domain.ErrValidation)
		}
		l.OwnerGroupID = gid
		if err := validateDestList(l); err != nil {
			return err
		}
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		var cur groupRow
		res := tx.Select("id", "dest_allowlist_state").Where("id = ?", gid).Limit(1).Find(&cur)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		if cur.DestAllowlistState != nil {
			return fmt.Errorf("%w: group %d already has an allowlist", domain.ErrAlreadyExists, gid)
		}
		ids := make([]int64, 0, 2)
		for _, l := range []*domain.DestList{base, extra} {
			row := destListToRow(l)
			row.ID = 0
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("create owned list: %w", err)
			}
			l.ID = row.ID
			ids = append(ids, row.ID)
		}
		a.BaseListID, a.ExtraListID = ids[0], ids[1]
		return saveAllowlistTx(tx, gid, a, nil)
	})
	if err != nil {
		base.ID, extra.ID, a.BaseListID, a.ExtraListID = 0, 0, 0, 0
		return err
	}
	return nil
}

// deleteGroupTx removes a group and what it owns: its row, and the lists it
// owns. The destination generation advances when the group had an allowlist or
// owned lists (the compiler could see them), and not otherwise.
func deleteGroupTx(tx *gorm.DB, id int64) error {
	if err := lockDestState(tx); err != nil {
		return err
	}
	var cur groupRow
	res := tx.Select("id", "dest_allowlist_state").Where("id = ?", id).Limit(1).Find(&cur)
	if res.Error != nil {
		return res.Error
	}
	owned := tx.Where("owner_group_id = ?", id).Delete(&destListRow{})
	if owned.Error != nil {
		return owned.Error
	}
	if err := tx.Delete(&groupRow{}, id).Error; err != nil {
		return err
	}
	if cur.DestAllowlistState != nil || owned.RowsAffected > 0 {
		return bumpDestGeneration(tx, time.Now())
	}
	return nil
}

// List reads the groups that have an allowlist row (in any state, off included),
// by group id.
func (r *DestGroupAllowlistRepo) List(ctx context.Context) ([]*domain.DestGroupAllowlistEntry, error) {
	var rows []groupRow
	if err := r.db.WithContext(ctx).
		Select("id", "name", "dest_allowlist_state", "dest_allowlist_list_ids", "dest_allowlist_base_list_id",
			"dest_allowlist_extra_list_id", "dest_allowlist_state_changed_at", "dest_allowlist_updated_at").
		Where("dest_allowlist_state IS NOT NULL").Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list group allowlists: %w", err)
	}
	out := make([]*domain.DestGroupAllowlistEntry, len(rows))
	for i := range rows {
		out[i] = &domain.DestGroupAllowlistEntry{GroupID: rows[i].ID, GroupName: rows[i].Name, Allowlist: rows[i].destAllowlist()}
	}
	return out, nil
}
