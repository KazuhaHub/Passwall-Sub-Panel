package render

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"

	"gopkg.in/yaml.v3"
)

func buildSingBoxGroupOutboundsWithMembers(rules string, items []renderItem, order []string, members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions) []map[string]any {
	return compileSingBoxGroups(rules, items, order, members, options, nil).Outbounds
}

func singBoxPassThroughProxyGroups(items []renderItem, members map[string][]domain.ProxyGroupMember, rules ...string) map[string]bool {
	return compileSingBoxGroups(strings.Join(rules, "\n"), items, nil, members, nil, nil).PassThrough
}

func (s *Service) buildSingBoxOutbounds(ctx context.Context, u *domain.User, items []renderItem, order []string, members map[string][]domain.ProxyGroupMember, options map[string]domain.ProxyGroupOptions, st ports.UISettings, rules ...string) []map[string]any {
	outbounds, items := s.buildSingBoxLineOutbounds(ctx, u, items, st, s.appliedClientCredentialsByNode(ctx, u))
	return append(outbounds, compileSingBoxGroups(strings.TrimSpace(strings.Join(rules, "\n")), items, order, members, options, nil).Outbounds...)
}

func (s *Service) renderURIList(ctx context.Context, u *domain.User, items []renderItem, st ports.UISettings) (*Output, error) {
	return s.renderURIListWithCredentials(ctx, u, items, st, s.appliedClientCredentialsByNode(ctx, u))
}

// Text adapters are test fixtures only. Production keeps a single parsed tree.
func pruneMihomoLines(body string, generated map[string]bool) (string, error) {
	document, err := parseMihomoSubscription(body)
	if err != nil {
		return "", err
	}
	if err := pruneMihomoDocument(document, generated, ""); err != nil {
		return "", err
	}
	raw, err := yaml.Marshal(document)
	return string(raw), err
}

func pruneSingBoxLines(body string, generated map[string]bool) (string, error) {
	config, err := parseSingBoxSubscription(body)
	if err != nil {
		return "", err
	}
	if err := pruneSingBoxDocument(config, generated, ""); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	return string(raw), err
}
