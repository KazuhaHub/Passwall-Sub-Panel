package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The reads the policy compiler adds to the destination store family.

// destReadOptions is the transaction mode of the definitions read. On
// PostgreSQL and MySQL the default is READ COMMITTED (PostgreSQL) or differs by
// engine settings, and a definitions read made of several statements must see
// ONE state of the tables: a write that committed between "read the
// generation" and "read the policies" would otherwise put a policy in the
// snapshot that the recorded generation does not cover (harmless: one more
// publish) — or, the other way round, leave a write out of a snapshot that
// claims to cover its generation, which would never be published. SQLite has a
// single connection, so its reads are consistent by construction, and its
// driver is not handed options it may refuse.
func destReadOptions(db *gorm.DB) *sql.TxOptions {
	switch db.Dialector.Name() {
	case "postgres", "mysql":
		return &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	return nil
}

// ReadDefinitions reads every definition the compiler publishes, and the
// generation they are consistent with. THE GENERATION IS READ FIRST, then the
// definitions, all in one repeatable-read read-only transaction: the snapshot
// then contains at least every write committed before that generation, and a
// write that landed later only causes one more harmless publish. (Read the
// other way round, a write between the two reads would leave the generation
// ahead of a body that lacks the write, and CompletePublish would record it as
// published: the change would never reach a node.)
//
// Lists carry their entries and not their pasted text: the text is the
// editor's, not the compiler's.
func (r *DestRepos) ReadDefinitions(ctx context.Context) (*domain.DestDefinitions, error) {
	defs := &domain.DestDefinitions{}
	body := func(tx *gorm.DB) error {
		var state destPolicyStateRow
		res := tx.Where("id = ?", destStateRowID).Limit(1).Find(&state)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			defs.Generation = state.Generation
		}
		var lists []destListRow
		if err := tx.Omit("SourceText").Order("id").Find(&lists).Error; err != nil {
			return err
		}
		var policies []destPolicyRow
		if err := tx.Order("action").Order("priority").Order("id").Find(&policies).Error; err != nil {
			return err
		}
		var exemptions []destExemptionRow
		if err := tx.Order("user_id").Find(&exemptions).Error; err != nil {
			return err
		}
		var rules []destSourceRuleRow
		if err := tx.Order("node_id").Find(&rules).Error; err != nil {
			return err
		}
		defs.Lists = make([]*domain.DestList, 0, len(lists))
		for _, row := range lists {
			defs.Lists = append(defs.Lists, row.toDomain())
		}
		defs.Policies = make([]*domain.DestPolicy, 0, len(policies))
		for _, row := range policies {
			defs.Policies = append(defs.Policies, row.toDomain())
		}
		defs.Exemptions = make([]*domain.DestExemption, 0, len(exemptions))
		for _, row := range exemptions {
			defs.Exemptions = append(defs.Exemptions, row.toDomain())
		}
		defs.SourceRules = make([]*domain.DestSourceRule, 0, len(rules))
		for _, row := range rules {
			defs.SourceRules = append(defs.SourceRules, row.toDomain())
		}
		return nil
	}
	db := r.db.WithContext(ctx)
	var err error
	if opts := destReadOptions(r.db); opts != nil {
		err = db.Transaction(body, opts)
	} else {
		err = db.Transaction(body)
	}
	if err != nil {
		return nil, fmt.Errorf("read destination definitions: %w", err)
	}
	return defs, nil
}

// groupLookupChunk bounds one IN list: SQLite refuses more than 32766 bound
// variables and the roster of a large panel is past that.
const groupLookupChunk = 1000

// GroupIDsByUserIDs maps each given user id to its group id. A user that does
// not exist is simply absent from the map. It lives here and not on UserRepo
// so the compiler's one question about users does not widen the user port (and
// every test double that implements it).
func (r *DestRepos) GroupIDsByUserIDs(ctx context.Context, ids []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(ids))
	for start := 0; start < len(ids); start += groupLookupChunk {
		end := min(start+groupLookupChunk, len(ids))
		var rows []struct {
			ID      int64
			GroupID int64
		}
		if err := r.db.WithContext(ctx).Model(&userRow{}).Select("id", "group_id").Where("id IN ?", ids[start:end]).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("read user groups: %w", err)
		}
		for _, row := range rows {
			out[row.ID] = row.GroupID
		}
	}
	return out, nil
}

// GetMeta reads one agent's delivery state WITHOUT the last-known-good body:
// the body is up to several MiB and only a fallback reads it.
func (r *DestAgentPolicyRepo) GetMeta(ctx context.Context, agentID string) (*domain.DestAgentPolicy, error) {
	var row destAgentPolicyRow
	res := r.db.WithContext(ctx).Omit("AppliedBody").Where("agent_id = ?", agentID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("get dest agent policy: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

// AppliedBody reads the last-known-good body alone. An agent with no row has
// none: nil, not an error.
func (r *DestAgentPolicyRepo) AppliedBody(ctx context.Context, agentID string) ([]byte, error) {
	var row destAgentPolicyRow
	res := r.db.WithContext(ctx).Select("agent_id", "applied_body").Where("agent_id = ?", agentID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("read dest agent applied body: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return row.AppliedBody, nil
}

// List reads every agent's delivery state, without the bodies.
func (r *DestAgentPolicyRepo) List(ctx context.Context) ([]*domain.DestAgentPolicy, error) {
	var rows []destAgentPolicyRow
	if err := r.db.WithContext(ctx).Omit("AppliedBody").Order("agent_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list dest agent policies: %w", err)
	}
	out := make([]*domain.DestAgentPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// CountUsersByGroup counts the accounts of each group, for the policy budget's
// subject estimate (a policy scoped to a group names that many subjects at
// most on any node).
func (r *DestRepos) CountUsersByGroup(ctx context.Context) (map[int64]int, error) {
	var rows []struct {
		GroupID int64
		N       int
	}
	if err := r.db.WithContext(ctx).Model(&userRow{}).Select("group_id, COUNT(*) AS n").Group("group_id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count users by group: %w", err)
	}
	out := make(map[int64]int, len(rows))
	for _, row := range rows {
		out[row.GroupID] = row.N
	}
	return out, nil
}
