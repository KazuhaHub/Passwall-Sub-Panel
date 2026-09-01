package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
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
func validateSSOGroupRules(ctx context.Context, groups ports.GroupRepo, rules []config.SSOGroupRule) error {
	if len(rules) == 0 {
		return nil
	}
	if groups == nil {
		// A handler built without the repo cannot check, and silently
		// skipping would make the guarantee conditional on wiring. Say so.
		return fmt.Errorf("group rules cannot be validated: no group repository wired")
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
			return fmt.Errorf("group rule %d: no group with slug %q", i+1, slug)
		}
		checked[slug] = true
	}
	return nil
}
