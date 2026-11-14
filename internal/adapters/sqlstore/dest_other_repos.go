package sqlstore

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DestRepos is the destination-policy store family, built once from the
// database handle and handed to each consumer as the narrow view it declares.
type DestRepos struct {
	db              *gorm.DB
	Lists           *DestListRepo
	Policies        *DestPolicyRepo
	Exemptions      *DestExemptionRepo
	SourceRules     *DestSourceRuleRepo
	GroupAllowlists *DestGroupAllowlistRepo
	State           *DestStateRepo
	Agents          *DestAgentPolicyRepo
	Hits            *DestHitRepo
}

// NewDestRepos builds the family over db.
func NewDestRepos(db *gorm.DB) *DestRepos {
	return &DestRepos{
		db:              db,
		Lists:           &DestListRepo{db: db},
		Policies:        &DestPolicyRepo{db: db},
		Exemptions:      &DestExemptionRepo{db: db},
		SourceRules:     &DestSourceRuleRepo{db: db},
		GroupAllowlists: &DestGroupAllowlistRepo{db: db},
		State:           &DestStateRepo{db: db},
		Agents:          &DestAgentPolicyRepo{db: db},
		Hits:            &DestHitRepo{db: db},
	}
}

// ---- exemptions ----

// DestExemptionRepo is the dest_exemptions store.
type DestExemptionRepo struct{ db *gorm.DB }

// Upsert creates or replaces a user's exemption.
func (r *DestExemptionRepo) Upsert(ctx context.Context, e *domain.DestExemption) error {
	switch {
	case e.UserID <= 0:
		return fmt.Errorf("%w: dest exemption needs a user", domain.ErrValidation)
	case len(e.Reason) > destReasonWidth:
		return fmt.Errorf("%w: dest exemption reason is %d bytes, over %d", domain.ErrValidation, len(e.Reason), destReasonWidth)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		created := time.Now().UTC()
		if !e.CreatedAt.IsZero() {
			created = e.CreatedAt.UTC()
		}
		row := destExemptionRow{UserID: e.UserID, Reason: e.Reason, CreatedBy: e.CreatedBy, CreatedAt: created, ExpiresAt: utcPtr(e.ExpiresAt)}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"reason", "created_by", "created_at", "expires_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
		return bumpDestGeneration(tx, time.Now())
	})
}

func (row destExemptionRow) toDomain() *domain.DestExemption {
	return &domain.DestExemption{
		UserID: row.UserID, Reason: row.Reason, CreatedBy: row.CreatedBy,
		CreatedAt: row.CreatedAt.UTC(), ExpiresAt: utcPtr(row.ExpiresAt),
	}
}

// Get reads one user's exemption.
func (r *DestExemptionRepo) Get(ctx context.Context, userID int64) (*domain.DestExemption, error) {
	var row destExemptionRow
	res := r.db.WithContext(ctx).Where("user_id = ?", userID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("get dest exemption: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

// List reads every exemption in user-id order.
func (r *DestExemptionRepo) List(ctx context.Context) ([]*domain.DestExemption, error) {
	var rows []destExemptionRow
	if err := r.db.WithContext(ctx).Order("user_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list dest exemptions: %w", err)
	}
	out := make([]*domain.DestExemption, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// Delete removes a user's exemption.
func (r *DestExemptionRepo) Delete(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		res := tx.Where("user_id = ?", userID).Delete(&destExemptionRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		return bumpDestGeneration(tx, time.Now())
	})
}

// deleteDestExemptionTx removes a user's exemption on tx and advances the
// generation when there was one: an exemption in force is leaving the policy.
// It is the user repository's half of the removal cleanup.
func deleteDestExemptionTx(tx *gorm.DB, userID int64) error {
	res := tx.Where("user_id = ?", userID).Delete(&destExemptionRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return bumpDestGeneration(tx, time.Now())
}

// DeleteExpired removes every exemption whose expiry is at or before now and
// returns how many. Expiry is a definition change — the exemption leaves the
// policy — so it advances the generation, but only when something expired.
func (r *DestExemptionRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		res := tx.Where("expires_at IS NOT NULL AND expires_at <= ?", now.UTC()).Delete(&destExemptionRow{})
		if res.Error != nil {
			return res.Error
		}
		n = res.RowsAffected
		if n == 0 {
			return nil
		}
		return bumpDestGeneration(tx, time.Now())
	})
	if err != nil {
		return 0, fmt.Errorf("delete expired dest exemptions: %w", err)
	}
	return n, nil
}

// ---- source rules ----

// DestSourceRuleRepo is the dest_source_rules store.
type DestSourceRuleRepo struct{ db *gorm.DB }

func validateDestSourceRule(rule *domain.DestSourceRule) ([]string, error) {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: dest source rule: "+format, append([]any{domain.ErrValidation}, args...)...)
	}
	if rule.NodeID <= 0 {
		return nil, fail("needs a node")
	}
	if !rule.State.Valid() {
		return nil, fail("unknown state %q", rule.State)
	}
	countries, ok := domain.NormalizeDestCountries(rule.Countries)
	if !ok {
		return nil, fail("countries must be ISO 3166-1 alpha-2 codes")
	}
	// CIDRs are stored exactly as canonical as the wire wants them (masked, in
	// bytewise order, once each): the compiler copies them into a document
	// that is validated in exactly that form, so a loose value saved here
	// would surface later as an unpublishable policy.
	for i, c := range rule.CIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil || p != p.Masked() || p.String() != c {
			return nil, fail("cidr %q is not a canonical prefix", c)
		}
		if i > 0 && rule.CIDRs[i-1] >= c {
			return nil, fail("cidrs must be sorted and without repeats")
		}
	}
	return countries, nil
}

// Upsert creates or replaces the rule of rule.NodeID. ifUpdatedAt, when
// given, is the precondition (see DestPolicyRepo.Update); a precondition on a
// row that does not exist is a conflict too, because the caller believed it
// was editing one.
func (r *DestSourceRuleRepo) Upsert(ctx context.Context, rule *domain.DestSourceRule, ifUpdatedAt *time.Time) error {
	countries, err := validateDestSourceRule(rule)
	if err != nil {
		return err
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDestState(tx); err != nil {
			return err
		}
		var cur destSourceRuleRow
		res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("node_id = ?", rule.NodeID).Limit(1).Find(&cur)
		if res.Error != nil {
			return res.Error
		}
		exists := res.RowsAffected == 1
		if ifUpdatedAt != nil && (!exists || cur.UpdatedAt.UnixMilli() != ifUpdatedAt.UnixMilli()) {
			return fmt.Errorf("%w: the source rule changed since it was read", domain.ErrConflict)
		}
		row := destSourceRuleRow{
			NodeID: rule.NodeID, Countries: jsonStrings(countries), CIDRs: jsonStrings(rule.CIDRs),
			State: string(rule.State), UpdatedAt: time.Now().UTC(),
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "node_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"countries", "cidrs", "state", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
		return bumpDestGeneration(tx, time.Now())
	})
	if err != nil {
		return err
	}
	rule.Countries, rule.UpdatedAt = countries, time.Now().UTC()
	if fresh, gerr := r.Get(ctx, rule.NodeID); gerr == nil {
		rule.UpdatedAt = fresh.UpdatedAt
	}
	return nil
}

// Get reads one node's rule.
func (r *DestSourceRuleRepo) Get(ctx context.Context, nodeID int64) (*domain.DestSourceRule, error) {
	var row destSourceRuleRow
	res := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("get dest source rule: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

// List reads every rule in node-id order.
func (r *DestSourceRuleRepo) List(ctx context.Context) ([]*domain.DestSourceRule, error) {
	var rows []destSourceRuleRow
	if err := r.db.WithContext(ctx).Order("node_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list dest source rules: %w", err)
	}
	out := make([]*domain.DestSourceRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

// deleteDestSourceRuleTx removes a node's rule on tx and, when there was one,
// advances the generation: a rule that was in force is leaving the policy. It
// is the node repository's half of the removal cleanup, so a node and its rule
// leave in one transaction.
func deleteDestSourceRuleTx(tx *gorm.DB, nodeID int64) error {
	res := tx.Where("node_id = ?", nodeID).Delete(&destSourceRuleRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return bumpDestGeneration(tx, time.Now())
}

// ---- agent policy ----

// DestAgentPolicyRepo is the dest_agent_policy store: one row per native node
// agent. Writers are column-scoped (Patch) because three independent writers
// own disjoint column groups; see domain.DestAgentPolicyPatch.
type DestAgentPolicyRepo struct{ db *gorm.DB }

// Get reads one agent's row.
func (r *DestAgentPolicyRepo) Get(ctx context.Context, agentID string) (*domain.DestAgentPolicy, error) {
	var row destAgentPolicyRow
	res := r.db.WithContext(ctx).Where("agent_id = ?", agentID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("get dest agent policy: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return row.toDomain(), nil
}

// Patch writes the columns patch names, creating the agent's row first if it
// has none. Everything is checked before anything is written.
func (r *DestAgentPolicyRepo) Patch(ctx context.Context, agentID string, patch domain.DestAgentPolicyPatch) error {
	if agentID == "" || len(agentID) > destAgentIDWidth {
		return fmt.Errorf("%w: dest agent policy: bad agent id", domain.ErrValidation)
	}
	set := map[string]any{}
	digest := func(col string, v *string) error {
		if v == nil {
			return nil
		}
		if len(*v) > destShaWidth {
			return fmt.Errorf("%w: dest agent policy: %s is %d bytes, over %d", domain.ErrValidation, col, len(*v), destShaWidth)
		}
		set[col] = *v
		return nil
	}
	short := func(col string, v *string, width int) error {
		if v == nil {
			return nil
		}
		if len(*v) > width {
			return fmt.Errorf("%w: dest agent policy: %s is %d bytes, over %d", domain.ErrValidation, col, len(*v), width)
		}
		set[col] = *v
		return nil
	}
	nullable := func(col string, v *string) {
		if v == nil {
			return
		}
		if *v == "" {
			set[col] = nil
		} else {
			set[col] = *v
		}
	}
	for _, step := range []error{
		digest("desired_sha256", patch.DesiredSHA256),
		digest("minted_sha256", patch.MintedSHA256),
		digest("applied_sha256", patch.AppliedSHA256),
		digest("reported_sha256", patch.ReportedSHA256),
		digest("rejected_sha256", patch.RejectedSHA256),
		digest("minted_ctx", patch.MintedCtx),
		digest("rejected_ctx", patch.RejectedCtx),
		short("collect_effective", patch.CollectEffective, 16),
		short("resolve_effective", patch.ResolveEffective, 16),
		short("resolve_unavailable", patch.ResolveUnavailable, 16),
		short("report_only_shed", patch.ReportOnlyShed, 16),
		short("applied_collect", patch.AppliedCollect, 16),
		short("reported_state", patch.ReportedState, 16),
		short("reported_issue", patch.ReportedIssue, 64),
		short("rejected_caps", patch.RejectedCaps, 8),
	} {
		if step != nil {
			return step
		}
	}
	if patch.FallbackReason != nil {
		if !patch.FallbackReason.Valid() {
			return fmt.Errorf("%w: dest agent policy: unknown fallback reason %q", domain.ErrValidation, *patch.FallbackReason)
		}
		set["fallback_reason"] = string(*patch.FallbackReason)
	}
	if patch.RejectedGeneration != nil {
		set["rejected_generation"] = *patch.RejectedGeneration
	}
	if patch.MintedGeneration != nil {
		set["minted_generation"] = *patch.MintedGeneration
	}
	if patch.AppliedRuleCount != nil {
		set["applied_rule_count"] = *patch.AppliedRuleCount
	}
	if patch.AppliedBody != nil {
		set["applied_body"] = *patch.AppliedBody
	}
	if patch.AppliedGroups != nil {
		set["applied_groups"] = jsonInt64s(*patch.AppliedGroups)
	}
	for col, v := range map[string]*time.Time{"minted_at": patch.MintedAt, "applied_at": patch.AppliedAt, "reported_at": patch.ReportedAt} {
		if v != nil {
			set[col] = v.UTC()
		}
	}
	nullable("over_limit", patch.OverLimit)
	nullable("precheck_listeners", patch.PrecheckListeners)
	nullable("sniffing_groups_dropped", patch.SniffingGroupsDropped)
	nullable("reported_listeners", patch.ReportedListeners)

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&destAgentPolicyRow{AgentID: agentID, AppliedGroups: jsonInt64s{}}).Error; err != nil {
			return err
		}
		set["updated_at"] = time.Now().UTC()
		return tx.Model(&destAgentPolicyRow{}).Where("agent_id = ?", agentID).Updates(set).Error
	})
}

// Delete removes an agent's row. A row that is not there is not an error: the
// callers are cleanups.
func (r *DestAgentPolicyRepo) Delete(ctx context.Context, agentID string) error {
	if err := r.db.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&destAgentPolicyRow{}).Error; err != nil {
		return fmt.Errorf("delete dest agent policy: %w", err)
	}
	return nil
}
