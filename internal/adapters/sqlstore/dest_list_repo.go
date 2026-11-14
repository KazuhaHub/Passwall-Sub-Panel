package sqlstore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DestListRepo is the dest_lists store.
type DestListRepo struct{ db *gorm.DB }

func validateDestList(l *domain.DestList) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: dest list: "+format, append([]any{domain.ErrValidation}, args...)...)
	}
	switch {
	case l.Name == "":
		return fail("name is empty")
	case len(l.Name) > destNameWidth:
		return fail("name is %d bytes, over %d", len(l.Name), destNameWidth)
	case !l.Kind.Valid():
		return fail("unknown kind %q", l.Kind)
	case l.Side != "" && !l.Side.Valid():
		return fail("unknown side %q", l.Side)
	case len(l.SourceURL) > destURLWidth:
		return fail("source url is %d bytes, over %d", len(l.SourceURL), destURLWidth)
	case len(l.GeositeCategory) > destCategoryWidth:
		return fail("geosite category is %d bytes, over %d", len(l.GeositeCategory), destCategoryWidth)
	case len(l.GeositeAttrs) > destAttrsWidth:
		return fail("geosite attrs are %d bytes, over %d", len(l.GeositeAttrs), destAttrsWidth)
	case l.ContentSHA256 != "" && !isHexDigest(l.ContentSHA256):
		return fail("content digest is not 64 lower-case hex characters")
	case l.EntryCount < 0 || l.RegexpCount < 0:
		return fail("entry counts must not be negative")
	case l.OwnerGroupID < 0:
		return fail("owner group id must not be negative")
	}
	return nil
}

func destListToRow(l *domain.DestList) destListRow {
	side := l.Side
	if side == "" {
		side = domain.DestSideDest
	}
	return destListRow{
		ID: l.ID, Name: l.Name, Kind: string(l.Kind), Side: string(side), SourceURL: l.SourceURL,
		GeositeCategory: l.GeositeCategory, GeositeAttrs: l.GeositeAttrs, Entries: l.Entries, SourceText: l.SourceText,
		EntryCount: l.EntryCount, RegexpCount: l.RegexpCount, ContentSHA256: l.ContentSHA256,
		LastFetchedAt: l.LastFetchedAt, LastError: truncateUTF8(l.LastError, destErrorWidth), OwnerGroupID: l.OwnerGroupID,
	}
}

// Create inserts a list and fills in its id and timestamps. A new list is not
// referenced by any policy yet, so it is not a change anyone could publish and
// the generation does not move.
func (r *DestListRepo) Create(ctx context.Context, l *domain.DestList) error {
	if err := validateDestList(l); err != nil {
		return err
	}
	row := destListToRow(l)
	row.ID = 0
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("create dest list: %w", err)
	}
	// The timestamps as the database kept them (milliseconds).
	var stored destListRow
	if err := r.db.WithContext(ctx).Select("id", "created_at", "updated_at").Where("id = ?", row.ID).Take(&stored).Error; err != nil {
		return fmt.Errorf("create dest list: %w", err)
	}
	l.ID, l.Side, l.CreatedAt, l.UpdatedAt = row.ID, domain.DestListSide(row.Side), stored.CreatedAt.UTC(), stored.UpdatedAt.UTC()
	return nil
}

// GetByID reads one list with its entries and pasted text.
func (r *DestListRepo) GetByID(ctx context.Context, id int64) (*domain.DestList, error) {
	var row destListRow
	res := r.db.WithContext(ctx).Where("id = ?", id).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("get dest list: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

// List reads every list, oldest first. The two large columns are left out
// unless withEntries: a list page needs the counts, not megabytes per row.
func (r *DestListRepo) List(ctx context.Context, withEntries bool) ([]*domain.DestList, error) {
	q := r.db.WithContext(ctx).Order("id")
	if !withEntries {
		q = q.Omit("Entries", "SourceText")
	}
	var rows []destListRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list dest lists: %w", err)
	}
	out := make([]*domain.DestList, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// Entries reads one list's entries alone.
func (r *DestListRepo) Entries(ctx context.Context, id int64) ([]byte, error) {
	var row destListRow
	res := r.db.WithContext(ctx).Select("id", "entries").Where("id = ?", id).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("read dest list entries: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.Entries, nil
}

// Update rewrites a list wholesale: name, source, pasted text, entries. It is
// the write of a custom list's editor; a refresh uses UpdateContent. The
// generation advances only when the entries' digest changed, because that is
// what the compiler can see (a rename is not).
func (r *DestListRepo) Update(ctx context.Context, l *domain.DestList) error {
	if err := validateDestList(l); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		var cur destListRow
		res := tx.Select("id", "content_sha256").Where("id = ?", l.ID).Limit(1).Find(&cur)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		row := destListToRow(l)
		if err := tx.Model(&destListRow{}).Where("id = ?", l.ID).
			Select("Name", "Kind", "Side", "SourceURL", "GeositeCategory", "GeositeAttrs", "Entries", "SourceText",
				"EntryCount", "RegexpCount", "ContentSHA256", "LastFetchedAt", "LastError", "OwnerGroupID", "UpdatedAt").
			Updates(&row).Error; err != nil {
			return err
		}
		if cur.ContentSHA256 != row.ContentSHA256 {
			return bumpDestGeneration(tx, time.Now())
		}
		return nil
	})
}

// UpdateContent stores what a refresh derived. It always records that the
// source was fetched (last_fetched_at) and clears the last error, but it only
// replaces the entries — and only then advances the generation — when the
// digest differs: F13, a refresh that finds the same bytes must not restart
// every core. It reports whether the content changed.
func (r *DestListRepo) UpdateContent(ctx context.Context, id int64, c domain.DestListContent) (bool, error) {
	if c.ContentSHA256 != "" && !isHexDigest(c.ContentSHA256) {
		return false, fmt.Errorf("%w: dest list: content digest is not 64 lower-case hex characters", domain.ErrValidation)
	}
	var changed bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// First, before anything is read: the in-use decision below must be
		// about the policies as they are when this write commits.
		if err := lockDestState(tx); err != nil {
			return err
		}
		var cur destListRow
		res := tx.Select("id", "content_sha256").Where("id = ?", id).Limit(1).Find(&cur)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		fetched := c.FetchedAt.UTC()
		set := map[string]any{"last_fetched_at": fetched, "last_error": ""}
		changed = cur.ContentSHA256 != c.ContentSHA256
		if changed {
			set["entries"] = c.Entries
			set["entry_count"] = c.EntryCount
			set["regexp_count"] = c.RegexpCount
			set["content_sha256"] = c.ContentSHA256
		}
		if err := tx.Model(&destListRow{}).Where("id = ?", id).Updates(set).Error; err != nil {
			return err
		}
		if !changed {
			return nil
		}
		// Only a list a non-off policy references is something the compiler
		// can see; a refresh of any other restarts nothing.
		_, inUse, err := inForcePoliciesTx(tx)
		if err != nil {
			return err
		}
		if inUse[id] {
			return bumpDestGeneration(tx, time.Now())
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// ApplyBatch lands a batch of list writes in ONE transaction and reports which
// lists' entries changed and whether the generation moved. The policies in
// force are read inside it and handed to batch.Decide, so a guard (a list in
// force may not turn broad or too large, the policy may not pass a limit) is
// judged against the rows the write sits beside, and a refusal aborts the lot.
// Content that matches the stored digest only stamps the fetch; the generation
// advances at most once, when a changed list is in use or Decide asks for it.
// A list the batch names that does not exist fails the whole batch.
func (r *DestListRepo) ApplyBatch(ctx context.Context, batch domain.DestListBatch) (changed []int64, bumped bool, err error) {
	for _, c := range batch.Contents {
		if c.ContentSHA256 != "" && !isHexDigest(c.ContentSHA256) {
			return nil, false, fmt.Errorf("%w: dest list: content digest is not 64 lower-case hex characters", domain.ErrValidation)
		}
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		changed, bumped = nil, false
		if err := lockDestState(tx); err != nil {
			return err
		}
		inForce, inUse, err := inForcePoliciesTx(tx)
		if err != nil {
			return err
		}
		var notes map[int64]string
		force := false
		if batch.Decide != nil {
			if notes, force, err = batch.Decide(inUse, inForce); err != nil {
				return err
			}
		}
		for _, c := range batch.Contents {
			var cur destListRow
			res := tx.Select("id", "content_sha256").Where("id = ?", c.ListID).Limit(1).Find(&cur)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return domain.ErrNotFound
			}
			set := map[string]any{"last_fetched_at": c.FetchedAt.UTC(), "last_error": ""}
			if cur.ContentSHA256 != c.ContentSHA256 {
				set["entries"] = c.Entries
				set["entry_count"] = c.EntryCount
				set["regexp_count"] = c.RegexpCount
				set["content_sha256"] = c.ContentSHA256
				changed = append(changed, c.ListID)
				if inUse[c.ListID] {
					bumped = true
				}
			}
			if err := tx.Model(&destListRow{}).Where("id = ?", c.ListID).Updates(set).Error; err != nil {
				return err
			}
		}
		for id, reason := range notes {
			if err := requireList(tx, id); err != nil {
				return err
			}
			if err := tx.Model(&destListRow{}).Where("id = ?", id).Update("last_error", truncateUTF8(reason, destErrorWidth)).Error; err != nil {
				return err
			}
		}
		if force {
			bumped = true
		}
		if batch.Release != "" {
			if err := tx.Model(&destPolicyStateRow{}).Where("id = ?", destStateRowID).Update("geosite_release", batch.Release).Error; err != nil {
				return err
			}
		}
		if bumped {
			return bumpDestGeneration(tx, time.Now())
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return changed, bumped, nil
}

// requireList is the existence check that does not lean on RowsAffected: MySQL
// reports rows CHANGED by an UPDATE, so writing a value a row already holds
// reads as "no such row".
func requireList(tx *gorm.DB, id int64) error {
	var n int64
	if err := tx.Model(&destListRow{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetFetchError records a failed refresh and leaves the entries alone: the
// last good ones keep being served, which is the point of recording the error
// instead of clearing the list.
func (r *DestListRepo) SetFetchError(ctx context.Context, id int64, reason string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireList(tx, id); err != nil {
			return err
		}
		return tx.Model(&destListRow{}).Where("id = ?", id).Update("last_error", truncateUTF8(reason, destErrorWidth)).Error
	})
}

// FindGeosite returns the category list with exactly these attrs that any
// policy may use, or ErrNotFound. A list a group owns is never offered: it may
// only be used inside that group.
func (r *DestListRepo) FindGeosite(ctx context.Context, category, attrs string) (*domain.DestList, error) {
	return findGeosite(r.db.WithContext(ctx), category, attrs)
}

func findGeosite(db *gorm.DB, category, attrs string) (*domain.DestList, error) {
	var row destListRow
	res := db.Where("kind = ? AND geosite_category = ? AND geosite_attrs = ? AND owner_group_id = 0 AND side = ?",
		string(domain.DestListGeosite), category, attrs, string(domain.DestSideDest)).
		Order("id").Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("find geosite list: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

// Delete removes a list. Whether a policy still uses it is the caller's check
// (Policies.UsingList): there is no foreign key to enforce it, like the rest
// of this schema.
func (r *DestListRepo) Delete(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Delete(&destListRow{}, id)
	if res.Error != nil {
		return fmt.Errorf("delete dest list: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteUnused deletes a list only if no policy references it, deciding and
// deleting in ONE transaction under the state-row lock that policy writes also
// take, so a policy cannot pick the list up between the check and the delete.
// When policies hold it, they are returned and nothing is deleted.
func (r *DestListRepo) DeleteUnused(ctx context.Context, id int64) (using domain.DestListUse, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		using = domain.DestListUse{}
		if err := lockDestState(tx); err != nil {
			return err
		}
		if err := requireList(tx, id); err != nil {
			return err
		}
		var rows []destPolicyRow
		if err := tx.Select("id", "list_ids").Order("id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if slices.Contains([]int64(row.ListIDs), id) {
				using.PolicyIDs = append(using.PolicyIDs, row.ID)
			}
		}
		// The allowlist groups hold lists too, in any state: an off group keeps
		// its lists for when it is switched on again.
		var groups []groupRow
		if err := tx.Select("id", "dest_allowlist_list_ids", "dest_allowlist_base_list_id", "dest_allowlist_extra_list_id").
			Where("dest_allowlist_state IS NOT NULL").Order("id").Find(&groups).Error; err != nil {
			return err
		}
		for _, g := range groups {
			a := g.destAllowlist()
			if a.BaseListID == id || a.ExtraListID == id || slices.Contains(a.ListIDs, id) {
				using.GroupIDs = append(using.GroupIDs, g.ID)
			}
		}
		if using.Any() {
			return nil
		}
		return tx.Delete(&destListRow{}, id).Error
	})
	if err != nil {
		return domain.DestListUse{}, err
	}
	return using, nil
}
