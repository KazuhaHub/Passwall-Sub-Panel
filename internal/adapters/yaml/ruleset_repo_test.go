package yaml

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestRuleSetRepoSaveListGetDelete(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.Save(ctx, &domain.RuleSet{
		Slug:    "b_rules",
		Name:    "B rules",
		Sort:    20,
		Enabled: true,
		Content: "- MATCH,DIRECT",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &domain.RuleSet{
		Slug:            "a_rules",
		Name:            "A rules",
		Sort:            10,
		Enabled:         false,
		ProxyGroupOrder: []string{"🚀 节点选择", "💬 Ai平台"},
		ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
			"💬 Ai平台": {{Kind: "node", NodeID: 42}, {Kind: "node_set", Value: "remaining"}},
		},
		ProxyGroupOptions: map[string]domain.ProxyGroupOptions{
			"💬 Ai平台": {Type: "url-test", URL: "https://example.com/check", Interval: ruleSetInt(0), Lazy: ruleSetBool(false), Timeout: ruleSetInt(2500), Tolerance: ruleSetInt(80)},
		},
		Content: "- DOMAIN-SUFFIX,example.com,DIRECT",
	}); err != nil {
		t.Fatal(err)
	}

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Slug != "a_rules" || items[1].Slug != "b_rules" {
		t.Fatalf("unexpected list order: %#v", items)
	}

	got, err := repo.GetBySlug(ctx, "a_rules")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "A rules" || got.Enabled || len(got.ProxyGroupOrder) != 2 || got.ProxyGroupOrder[1] != "💬 Ai平台" {
		t.Fatalf("unexpected ruleset: %#v", got)
	}
	if members := got.ProxyGroupMembers["💬 Ai平台"]; len(members) != 2 || members[0].NodeID != 42 || members[1].Value != "remaining" {
		t.Fatalf("unexpected proxy group members: %#v", got.ProxyGroupMembers)
	}
	if options := got.ProxyGroupOptions["💬 Ai平台"]; options.Type != "url-test" || options.Interval == nil || *options.Interval != 0 || options.Lazy == nil || *options.Lazy || options.Tolerance == nil || *options.Tolerance != 80 {
		t.Fatalf("unexpected proxy group options: %#v", got.ProxyGroupOptions)
	}

	if _, err := os.Stat(filepath.Join(repo.dir, "a_rules.yaml")); err != nil {
		t.Fatalf("expected ruleset file: %v", err)
	}
	if err := repo.Delete(ctx, "a_rules"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetBySlug(ctx, "a_rules"); err != domain.ErrNotFound {
		t.Fatalf("GetBySlug deleted err = %v, want ErrNotFound", err)
	}
}

func ruleSetInt(value int) *int    { return &value }
func ruleSetBool(value bool) *bool { return &value }

func TestRuleSetRepoGetBySlugUsesDocumentSlug(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	raw := []byte(`slug: default_rules
name: Default Rules
sort: 10
enabled: true
proxy_group_order:
  - 🚀 节点选择
content: |
  - MATCH,DIRECT
`)
	if err := os.WriteFile(filepath.Join(repo.dir, "default-rules.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetBySlug(ctx, "default_rules")
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "default_rules" || got.Content != "- MATCH,DIRECT\n" || len(got.ProxyGroupOrder) != 1 || got.ProxyGroupOrder[0] != "🚀 节点选择" {
		t.Fatalf("unexpected ruleset: %#v", got)
	}
	if err := repo.Delete(ctx, "default_rules"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo.dir, "default-rules.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected scanned file to be deleted, stat err = %v", err)
	}
}

// TestRuleSetRepoSaveOverwritesMismatchedFilename locks the beta.8+ fix for the
// duplicate-default-rules bug: the seeded default ships as default-rules.yaml
// (hyphen) but carries slug "default_rules" (underscore). Save used to resolve
// by slug -> filename only, writing a SECOND file default_rules.yaml next to the
// seed, so the list surfaced two rows sharing slug "default_rules". Save must now
// resolve like GetBySlug/Delete (by document slug) and overwrite the SAME file.
func TestRuleSetRepoSaveOverwritesMismatchedFilename(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	raw := []byte("slug: default_rules\nname: Default Rules\nsort: 10\nenabled: true\ncontent: |\n  - MATCH,DIRECT\n")
	if err := os.WriteFile(filepath.Join(repo.dir, "default-rules.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	// Edit the seeded default (same slug).
	if err := repo.Save(ctx, &domain.RuleSet{
		Slug:    "default_rules",
		Name:    "Default Rules (edited)",
		Sort:    10,
		Enabled: true,
		Content: "- MATCH,REJECT",
	}); err != nil {
		t.Fatal(err)
	}

	// Exactly one file remains, and it is the original default-rules.yaml — no
	// stray default_rules.yaml was created.
	entries, err := os.ReadDir(repo.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "default-rules.yaml" {
		t.Fatalf("after editing seeded default: files = %v, want exactly [default-rules.yaml] (no duplicate)", names)
	}

	// List shows one row; the edit landed in default-rules.yaml.
	items, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("List len = %d, want 1 (no duplicate-slug rows)", len(items))
	}
	got, err := repo.GetBySlug(ctx, "default_rules")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Default Rules (edited)" {
		t.Fatalf("edit not applied to the resolved file: %#v", got)
	}
}

func TestRuleSetRepoRejectsUnsafeSlug(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	err = repo.Save(ctx, &domain.RuleSet{
		Slug:    "../escape",
		Name:    "Escape",
		Enabled: true,
		Content: "- MATCH,DIRECT",
	})
	if err == nil {
		t.Fatal("Save with unsafe slug succeeded, want error")
	}
}
