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
		Content:         "- DOMAIN-SUFFIX,example.com,DIRECT",
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
