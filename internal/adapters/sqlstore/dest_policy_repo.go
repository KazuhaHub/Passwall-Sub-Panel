package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DestPolicyRepo is the dest_policies store. Every write is a DEFINITION write:
// it advances the destination generation in its own transaction.
type DestPolicyRepo struct{ db *gorm.DB }

func validateDestPolicy(p *domain.DestPolicy) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: dest policy: "+format, append([]any{domain.ErrValidation}, args...)...)
	}
	switch {
	case p.Name == "":
		return fail("name is empty")
	case len(p.Name) > destNameWidth:
		return fail("name is %d bytes, over %d", len(p.Name), destNameWidth)
	case len(p.Description) > destDescriptionWidth:
		return fail("description is %d bytes, over %d", len(p.Description), destDescriptionWidth)
	case !p.Action.Valid():
		return fail("unknown action %q", p.Action)
	case !p.State.Valid():
		return fail("unknown state %q", p.State)
	case !p.Scope.Valid():
		return fail("unknown scope %q", p.Scope)
	case len(p.TemplateKey) > destTemplateKeyWidth:
		return fail("template key is %d bytes, over %d", len(p.TemplateKey), destTemplateKeyWidth)
	case p.Priority < 0:
		return fail("priority must not be negative")
	case p.Scope == domain.DestScopeGroups && len(p.GroupIDs) == 0:
		return fail("scope groups needs at least one group")
	case p.Scope == domain.DestScopePanels && len(p.PanelIDs) == 0:
		return fail("scope panels needs at least one panel")
	}
	for _, ids := range []struct {
		field string
		ids   []int64
	}{{"list_ids", p.ListIDs}, {"group_ids", p.GroupIDs}, {"panel_ids", p.PanelIDs}} {
		seen := make(map[int64]bool, len(ids.ids))
		for _, id := range ids.ids {
			if id <= 0 {
				return fail("%s holds a non-positive id", ids.field)
			}
			if seen[id] {
				return fail("%s lists %d twice", ids.field, id)
			}
			seen[id] = true
		}
	}
	return nil
}

func destPolicyToRow(p *domain.DestPolicy) destPolicyRow {
	return destPolicyRow{
		ID: p.ID, Name: p.Name, Description: p.Description, Action: string(p.Action), ListIDs: jsonInt64s(p.ListIDs),
		Inline: destInlineJSON(p.Inline), State: string(p.State), Scope: string(p.Scope),
		GroupIDs: jsonInt64s(p.GroupIDs), PanelIDs: jsonInt64s(p.PanelIDs), Priority: p.Priority,
		CountsAsRisk: p.CountsAsRisk, TemplateKey: p.TemplateKey,
	}
}

// mapPolicyWriteError turns a duplicate name into the domain's own conflict.
func mapPolicyWriteError(err error) error {
	if isUniqueViolationErr(err) {
		return fmt.Errorf("%w: a policy with this name exists", domain.ErrAlreadyExists)
	}
	return err
}

// createPolicyTx inserts p on tx: it assigns the next priority of the action
// when none was given, and advances the generation. Shared with the template
// instantiation, which must create lists and the policy in one transaction.
func createPolicyTx(tx *gorm.DB, p *domain.DestPolicy) error {
	if err := validateDestPolicy(p); err != nil {
		return err
	}
	if err := lockDestState(tx); err != nil {
		return err
	}
	if err := requireFreeName(tx, p.Name, 0); err != nil {
		return err
	}
	if err := requireListsExist(tx, p.ListIDs); err != nil {
		return err
	}
	row := destPolicyToRow(p)
	row.ID = 0
	if row.Priority == 0 {
		var top sql.NullInt64
		if err := tx.Model(&destPolicyRow{}).Where("action = ?", row.Action).
			Select("MAX(priority)").Scan(&top).Error; err != nil {
			return err
		}
		row.Priority = 1
		if top.Valid {
			row.Priority = int(top.Int64) + 1
		}
	}
	if err := tx.Create(&row).Error; err != nil {
		return mapPolicyWriteError(err)
	}
	if err := bumpDestGeneration(tx, time.Now()); err != nil {
		return err
	}
	// Read the timestamps back: the database keeps milliseconds, and the next
	// PUT's precondition is compared against what a read returns.
	var stored destPolicyRow
	if err := tx.Select("id", "created_at", "updated_at").Where("id = ?", row.ID).Take(&stored).Error; err != nil {
		return err
	}
	p.ID, p.Priority, p.CreatedAt, p.UpdatedAt = row.ID, row.Priority, stored.CreatedAt.UTC(), stored.UpdatedAt.UTC()
	return nil
}

// requireListsExist refuses a policy that names a list that does not exist,
// checked inside the locked transaction: a list deleted (DeleteUnused takes the
// same lock) between the admin's read and this write would otherwise leave a
// policy pointing at nothing.
func requireListsExist(tx *gorm.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	var n int64
	if err := tx.Model(&destListRow{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
		return err
	}
	if int(n) != len(ids) {
		return fmt.Errorf("%w: dest policy: a list it names does not exist", domain.ErrValidation)
	}
	return nil
}

// requireFreeName refuses a name another policy holds, compared with case
// folded HERE so every dialect agrees: MySQL's collation folds, PostgreSQL's
// and SQLite's do not. The unique index remains the backstop for a race the
// state-row lock does not cover.
func requireFreeName(tx *gorm.DB, name string, exceptID int64) error {
	var rows []destPolicyRow
	if err := tx.Select("id", "name").Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		if r.ID != exceptID && strings.EqualFold(r.Name, name) {
			return fmt.Errorf("%w: a policy with this name exists", domain.ErrAlreadyExists)
		}
	}
	return nil
}

// inForcePoliciesTx reads the policies that are not off and the set of list
// ids they reference, on tx.
func inForcePoliciesTx(tx *gorm.DB) ([]*domain.DestPolicy, map[int64]bool, error) {
	var rows []destPolicyRow
	if err := tx.Where("state <> ?", string(domain.DestStateOff)).Order("id").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	out := make([]*domain.DestPolicy, 0, len(rows))
	inUse := map[int64]bool{}
	for _, r := range rows {
		out = append(out, r.toDomain())
		for _, id := range r.ListIDs {
			inUse[id] = true
		}
	}
	// The lists an allowlist group that is not off uses are in use too, though
	// the group is not among the policies returned.
	var groups []groupRow
	if err := tx.Select("id", "dest_allowlist_state", "dest_allowlist_list_ids", "dest_allowlist_base_list_id", "dest_allowlist_extra_list_id").
		Where("dest_allowlist_state IS NOT NULL AND dest_allowlist_state <> ?", string(domain.DestStateOff)).Find(&groups).Error; err != nil {
		return nil, nil, err
	}
	for _, g := range groups {
		a := g.destAllowlist()
		for _, id := range append([]int64{a.BaseListID, a.ExtraListID}, a.ListIDs...) {
			if id > 0 {
				inUse[id] = true
			}
		}
	}
	return out, inUse, nil
}

// Create inserts a policy and fills in its id, priority and timestamps. A
// zero Priority means "last of its action".
func (r *DestPolicyRepo) Create(ctx context.Context, p *domain.DestPolicy) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return createPolicyTx(tx, p) })
}

func (r *DestPolicyRepo) one(ctx context.Context, where string, arg any) (*domain.DestPolicy, error) {
	var row destPolicyRow
	res := r.db.WithContext(ctx).Where(where, arg).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("get dest policy: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

// GetByID reads one policy.
func (r *DestPolicyRepo) GetByID(ctx context.Context, id int64) (*domain.DestPolicy, error) {
	return r.one(ctx, "id = ?", id)
}

// GetByName reads one policy by its (unique) name.
func (r *DestPolicyRepo) GetByName(ctx context.Context, name string) (*domain.DestPolicy, error) {
	return r.one(ctx, "name = ?", name)
}

// List reads every policy in evaluation order within each action: blocks
// before allows is the compiler's business, so the order here is only a
// stable one (action, priority, id).
func (r *DestPolicyRepo) List(ctx context.Context) ([]*domain.DestPolicy, error) {
	var rows []destPolicyRow
	if err := r.db.WithContext(ctx).Order("action").Order("priority").Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list dest policies: %w", err)
	}
	out := make([]*domain.DestPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// Update rewrites a policy. ifUpdatedAt, when given, is the precondition: the
// admin's copy was read at that updated_at, and a row that changed since is a
// domain.ErrConflict — the PUT that would otherwise silently undo another
// admin's edit. It is compared in milliseconds, the finest precision every
// dialect keeps. created_at and priority are not the editor's to change (the
// service reorders through Reorder), so a nil ifUpdatedAt skips only the
// check, never those.
func (r *DestPolicyRepo) Update(ctx context.Context, p *domain.DestPolicy, ifUpdatedAt *time.Time) error {
	if err := validateDestPolicy(p); err != nil {
		return err
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		var cur destPolicyRow
		res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", p.ID).Limit(1).Find(&cur)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		if ifUpdatedAt != nil && cur.UpdatedAt.UnixMilli() != ifUpdatedAt.UnixMilli() {
			return fmt.Errorf("%w: the policy changed since it was read", domain.ErrConflict)
		}
		if err := requireFreeName(tx, p.Name, p.ID); err != nil {
			return err
		}
		if err := requireListsExist(tx, p.ListIDs); err != nil {
			return err
		}
		row := destPolicyToRow(p)
		row.Priority = cur.Priority
		if row.Action != cur.Action {
			// A policy that changes action joins the END of its new action's
			// order: a priority only means something among the policies of
			// one action, and inheriting the old number would drop it into
			// the middle of a list it never took part in.
			var top sql.NullInt64
			if err := tx.Model(&destPolicyRow{}).Where("action = ?", row.Action).
				Select("MAX(priority)").Scan(&top).Error; err != nil {
				return err
			}
			row.Priority = 1
			if top.Valid {
				row.Priority = int(top.Int64) + 1
			}
		}
		if err := tx.Model(&destPolicyRow{}).Where("id = ?", p.ID).
			Select("Name", "Description", "Action", "ListIDs", "Inline", "State", "Scope", "GroupIDs", "PanelIDs",
				"Priority", "CountsAsRisk", "TemplateKey", "UpdatedAt").
			Updates(&row).Error; err != nil {
			return mapPolicyWriteError(err)
		}
		// The name, the description and counts_as_risk are not compiled: an edit of only those
		// must not republish (and so must not restart a core).
		if compiledPolicyEqual(cur, row) {
			return nil
		}
		return bumpDestGeneration(tx, time.Now())
	})
	if err != nil {
		return err
	}
	if fresh, gerr := r.GetByID(ctx, p.ID); gerr == nil {
		p.Priority, p.CreatedAt, p.UpdatedAt = fresh.Priority, fresh.CreatedAt, fresh.UpdatedAt
	}
	return nil
}

// compiledPolicyEqual reports whether two rows differ in nothing the compiler
// reads. Priority is compared too: it orders the rules. CountsAsRisk is not:
// only the risk signal reads it, so an edit of it alone is not a new policy.
func compiledPolicyEqual(a, b destPolicyRow) bool {
	return a.Action == b.Action && a.State == b.State && a.Scope == b.Scope && a.Priority == b.Priority &&
		slices.Equal([]int64(a.ListIDs), []int64(b.ListIDs)) &&
		slices.Equal([]int64(a.GroupIDs), []int64(b.GroupIDs)) &&
		slices.Equal([]int64(a.PanelIDs), []int64(b.PanelIDs)) &&
		reflect.DeepEqual(domain.DestPolicyInline(a.Inline), domain.DestPolicyInline(b.Inline))
}

// Delete removes a policy.
func (r *DestPolicyRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		res := tx.Delete(&destPolicyRow{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		return bumpDestGeneration(tx, time.Now())
	})
}

// Reorder sets the priority of every policy of one action to the position of
// its id in ids. ids must be exactly that action's policies: an id of another
// action, an unknown one, a repeat, or a policy left out refuses the whole
// reorder, so a stale page cannot silently drop a policy to the end.
func (r *DestPolicyRepo) Reorder(ctx context.Context, action domain.DestAction, ids []int64) error {
	if !action.Valid() {
		return fmt.Errorf("%w: dest policy: unknown action %q", domain.ErrValidation, action)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		var have []int64
		if err := tx.Model(&destPolicyRow{}).Where("action = ?", string(action)).Pluck("id", &have).Error; err != nil {
			return err
		}
		want := slices.Clone(ids)
		slices.Sort(want)
		slices.Sort(have)
		if !slices.Equal(slices.Compact(want), have) || len(want) != len(ids) {
			return fmt.Errorf("%w: dest policy: the order must list exactly the %s policies, each once", domain.ErrValidation, action)
		}
		var cur []destPolicyRow
		if err := tx.Select("id", "priority").Where("action = ?", string(action)).Find(&cur).Error; err != nil {
			return err
		}
		was := make(map[int64]int, len(cur))
		for _, c := range cur {
			was[c.ID] = c.Priority
		}
		moved := false
		for i, id := range ids {
			if was[id] == i+1 {
				continue
			}
			moved = true
			if err := tx.Model(&destPolicyRow{}).Where("id = ?", id).Update("priority", i+1).Error; err != nil {
				return err
			}
		}
		if !moved {
			return nil
		}
		return bumpDestGeneration(tx, time.Now())
	})
}

// UsingList returns the ids of the policies that reference list id, in id
// order. list_ids is JSON text, so the scan is in Go; there are tens of
// policies, not thousands.
func (r *DestPolicyRepo) UsingList(ctx context.Context, listID int64) ([]int64, error) {
	var rows []destPolicyRow
	if err := r.db.WithContext(ctx).Select("id", "list_ids").Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("find policies using list: %w", err)
	}
	var out []int64
	for _, row := range rows {
		if slices.Contains([]int64(row.ListIDs), listID) {
			out = append(out, row.ID)
		}
	}
	return out, nil
}

// InstantiateTemplate creates the lists a catalog template needs and the
// policy that uses them, in ONE transaction: an interrupted instantiation
// leaves neither a policy without its lists nor lists nobody asked for.
//
// A list identical to an existing one — same geosite category and attrs, a
// destination list no group owns — is reused instead of created, so two
// templates that share a category share its list (and its refresh). The
// policy's ListIDs are set to the resulting ids in the order given. A second
// policy from the same template_key is refused with domain.ErrDestTemplateExists (an ErrAlreadyExists): the page
// shows a template as added once, and two copies would double its quota.
func (r *DestRepos) InstantiateTemplate(ctx context.Context, lists []domain.DestList, p *domain.DestPolicy) (*domain.DestPolicy, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Two instantiations of one template queue on the state row, so the
		// check below and the insert it guards cannot interleave.
		if err := lockDestState(tx); err != nil {
			return err
		}
		if p.TemplateKey != "" {
			var n int64
			if err := tx.Model(&destPolicyRow{}).Where("template_key = ?", p.TemplateKey).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return fmt.Errorf("%w: template %q was already added", domain.ErrDestTemplateExists, p.TemplateKey)
			}
		}
		ids := make([]int64, 0, len(lists))
		for i := range lists {
			l := lists[i]
			if l.Kind == domain.DestListGeosite {
				if found, err := findGeosite(tx, l.GeositeCategory, l.GeositeAttrs); err == nil {
					ids = append(ids, found.ID)
					continue
				} else if !errors.Is(err, domain.ErrNotFound) {
					return err
				}
			}
			if err := validateDestList(&l); err != nil {
				return err
			}
			row := destListToRow(&l)
			row.ID = 0
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("create dest list: %w", err)
			}
			ids = append(ids, row.ID)
		}
		p.ListIDs = ids
		return createPolicyTx(tx, p)
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}
