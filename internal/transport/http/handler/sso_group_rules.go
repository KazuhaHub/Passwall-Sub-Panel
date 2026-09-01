package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// validateSSOGroupRules rejects a rule set that cannot do what it says.
//
// It exists because every way a group rule can be wrong surfaces somewhere
// far from where it was typed: a rule naming a group that does not exist
// refuses to provision a new principal and is skipped for an existing one,
// and both of those happen at somebody's login, hours later, to somebody
// else. The whole point of checking here is to turn that into a form error.
//
// Deliberately NOT validated: whether the attribute or value corresponds to
// anything the IdP actually emits. The panel has no way to know a directory's
// group names ahead of a login, and guessing would reject correct rules.
//
// Both handlers share this. Two copies of "what makes a rule valid" would be
// free to disagree, and the SAML and OIDC forms would then accept different
// things for no reason a user could see.
func validateSSOGroupRules(ctx context.Context, groups ports.GroupRepo, rules []config.SSOGroupRule, defaultGroupSlug string) error {
	if len(rules) == 0 {
		return nil
	}
	if groups == nil {
		// A handler built without the repo cannot check, and silently
		// skipping would make the guarantee conditional on wiring. Say so.
		return fmt.Errorf("group rules cannot be validated: no group repository wired")
	}
	// A rule set can DEMOTE: a principal the IdP stops claiming falls back
	// to the default group. With no default group configured there is
	// nowhere to fall back to, and a user in no group inherits nothing —
	// which resolves to unlimited traffic and no connection caps. Storing
	// rules without a destination would arm a revocation that hands out
	// more than it takes away, so it is refused here rather than guarded
	// only at login time.
	if strings.TrimSpace(defaultGroupSlug) == "" {
		return fmt.Errorf("group rules require a default group: it is where a user goes when the IdP stops matching any rule")
	}
	if _, err := groups.GetBySlug(ctx, strings.TrimSpace(defaultGroupSlug)); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("default group %q does not exist", defaultGroupSlug)
		}
		return fmt.Errorf("checking default group %q: %w", defaultGroupSlug, err)
	}
	// Resolve each distinct slug once — a rule set typically points several
	// IdP groups at the same OU.
	checked := map[string]bool{}
	for i, r := range rules {
		slug := strings.TrimSpace(r.Group)
		if slug == "" {
			return fmt.Errorf("group rule %d: group is required", i+1)
		}
		if strings.TrimSpace(r.Value) == "" {
			// The same guard the matcher applies, surfaced as a message
			// instead of a rule that silently never fires. Without it an
			// admin can save a rule, watch it do nothing, and have no way
			// to tell that from a directory that is not sending the claim.
			return fmt.Errorf("group rule %d: value is required", i+1)
		}
		if checked[slug] {
			continue
		}
		if _, err := groups.GetBySlug(ctx, slug); err != nil {
			// Only a genuine miss means the slug is wrong. Reporting a
			// dropped connection as "no group with slug X" sends an admin
			// hunting for a typo in a value that is correct.
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("group rule %d: no group with slug %q", i+1, slug)
			}
			return fmt.Errorf("group rule %d: checking group %q: %w", i+1, slug, err)
		}
		checked[slug] = true
	}
	return nil
}
