package render

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// GroupSubscriptionPreview exposes only labels, never connection credentials.
type GroupSubscriptionPreview struct {
	GroupID int64                           `json:"group_id"`
	Status  string                          `json:"status"`
	Groups  []GroupSubscriptionPreviewGroup `json:"groups"`
}

type GroupSubscriptionPreviewGroup struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
}

// Keep compilation shared, but scope the simulation to exactly one rule set.
// Template-owned proxies, providers and other rule sets must not influence it.
const ruleSetPreviewTemplate = `proxies:
  {{ proxies }}
proxy-groups:
  {{ proxy_groups }}
rules:
  {{ rules_common }}
sub-rules:
  {{ sub_rules }}
`

// PreviewMihomoGroup compiles only the current draft using the selected group's
// settings and layout. It neither publishes this document nor enters the cache.
func (s *Service) PreviewMihomoGroup(ctx context.Context, groupID int64, draft *domain.RuleSet) (*GroupSubscriptionPreview, error) {
	if groupID <= 0 || draft == nil || draft.Slug == "" {
		return nil, fmt.Errorf("%w: group and draft slug required", domain.ErrValidation)
	}
	tpl := &domain.Template{Slug: "rule-set-preview", Content: ruleSetPreviewTemplate, RuleSets: []string{draft.Slug}}
	result := &GroupSubscriptionPreview{GroupID: groupID, Status: "ready", Groups: []GroupSubscriptionPreviewGroup{}}
	if !draft.Enabled {
		result.Status = "disabled"
	}
	// Values exist only for protocol generation. No PSPClient records are read,
	// written or impersonated; personal rules are deliberately absent.
	u := &domain.User{GroupID: groupID, UUID: "00000000-0000-4000-8000-000000000001", UPN: "group-preview", DisplayName: "group-preview"}
	output, err := s.renderForContext(ctx, u, domain.ClientMihomo, &previewContext{ruleSets: []*domain.RuleSet{draft}, template: tpl})
	if err != nil {
		return nil, err
	}
	var config struct {
		Groups []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Members []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(output.Body, &config); err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	advanced := MihomoRuleFeatures{SubRules: draft.MihomoSubRules, RematchOutbounds: draft.MihomoRematchOutbounds}
	for _, name := range proxyGroupTargets(draft.Content, draft.ProxyGroupMembers, advanced) {
		targets[name] = true
	}
	for _, group := range config.Groups {
		if !targets[group.Name] {
			continue
		}
		if group.Members == nil {
			group.Members = []string{}
		}
		result.Groups = append(result.Groups, GroupSubscriptionPreviewGroup{Name: group.Name, Type: group.Type, Members: group.Members})
	}
	return result, nil
}
