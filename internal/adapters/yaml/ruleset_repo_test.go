package yaml

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
		Slug:                     "a_rules",
		Name:                     "A rules",
		Sort:                     10,
		Enabled:                  false,
		DirectSubscriptionDomain: true,
		ProxyGroupOrder:          []string{"🚀 节点选择", "💬 Ai平台"},
		ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
			"💬 Ai平台": {{Kind: "node", NodeID: 42}, {Kind: "node_set", Value: "remaining"}},
		},
		ProxyGroupOptions: map[string]domain.ProxyGroupOptions{
			"💬 Ai平台": {Type: "url-test", URL: "https://example.com/check", Interval: ruleSetInt(0), Lazy: ruleSetBool(false), Timeout: ruleSetInt(2500), Tolerance: ruleSetInt(80)},
		},
		MihomoSubRules:         []domain.MihomoSubRule{{Name: "ai-rules", Content: "- MATCH,💬 Ai平台"}},
		MihomoRematchOutbounds: []domain.MihomoRematchOutbound{{Name: "use-ai-rules", TargetSubRule: "ai-rules"}},
		Content:                "- SUB-RULE,(NETWORK,tcp),ai-rules\n- DOMAIN-SUFFIX,example.com,DIRECT",
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
	if got.Name != "A rules" || got.Enabled || !got.DirectSubscriptionDomain || len(got.ProxyGroupOrder) != 2 || got.ProxyGroupOrder[1] != "💬 Ai平台" {
		t.Fatalf("unexpected ruleset: %#v", got)
	}
	if members := got.ProxyGroupMembers["💬 Ai平台"]; len(members) != 2 || members[0].NodeID != 42 || members[1].Value != "remaining" {
		t.Fatalf("unexpected proxy group members: %#v", got.ProxyGroupMembers)
	}
	if options := got.ProxyGroupOptions["💬 Ai平台"]; options.Type != "url-test" || options.Interval == nil || *options.Interval != 0 || options.Lazy == nil || *options.Lazy || options.Tolerance == nil || *options.Tolerance != 80 {
		t.Fatalf("unexpected proxy group options: %#v", got.ProxyGroupOptions)
	}
	if got.Content != "- SUB-RULE,(NETWORK,tcp),ai-rules\n- DOMAIN-SUFFIX,example.com,DIRECT" || len(got.MihomoSubRules) != 1 || got.MihomoSubRules[0].Name != "ai-rules" || len(got.MihomoRematchOutbounds) != 1 || got.MihomoRematchOutbounds[0].TargetSubRule != "ai-rules" {
		t.Fatalf("unexpected Mihomo advanced fields: %#v", got)
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

func TestRuleSetRepoReadsLegacyMihomoRulesAndWritesUnifiedContent(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRuleSetRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo.dir, "legacy.yaml")
	raw := []byte(`slug: legacy
name: Legacy
enabled: true
mihomo_rules: |-
  - REMATCH-NAME,marked,Proxy
  - SUB-RULE,(NETWORK,tcp),tcp-rules
content: |-
  - MATCH,DIRECT
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetBySlug(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	want := "- REMATCH-NAME,marked,Proxy\n- SUB-RULE,(NETWORK,tcp),tcp-rules\n- MATCH,DIRECT"
	if got.Content != want {
		t.Fatalf("merged content = %q, want %q", got.Content, want)
	}
	if err := repo.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "mihomo_rules:") || !strings.Contains(string(saved), "REMATCH-NAME,marked,Proxy") {
		t.Fatalf("legacy field was not migrated:\n%s", saved)
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

func newRevisionRepo(t *testing.T) (*RuleSetRepo, string) {
	t.Helper()
	root := t.TempDir()
	repo, err := NewRuleSetRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), &domain.RuleSet{Slug: "rev", Name: "Rev", Enabled: true, Content: "- MATCH,DIRECT"}); err != nil {
		t.Fatal(err)
	}
	return repo, filepath.Join(root, "rulesets", "rev.yaml")
}

func TestRuleSetRevisionStableAcrossReadsAndMatchesFileBytes(t *testing.T) {
	ctx := context.Background()
	repo, path := newRevisionRepo(t)
	a, err := repo.GetBySlug(ctx, "rev")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := repo.GetBySlug(ctx, "rev")
	if len(a.Revision) != 16 || a.Revision != b.Revision {
		t.Fatalf("revision unstable or wrong length: %q vs %q", a.Revision, b.Revision)
	}
	raw, _ := os.ReadFile(path)
	if want := revisionOf(raw); a.Revision != want {
		t.Fatalf("revision %q != hash of file bytes %q", a.Revision, want)
	}
	list, _ := repo.List(ctx)
	if len(list) != 1 || list[0].Revision != a.Revision {
		t.Fatalf("list revision mismatch: %#v", list)
	}
}

func TestRuleSetRevisionChangesAfterSaveAndSaveReportsIt(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRevisionRepo(t)
	before, _ := repo.GetBySlug(ctx, "rev")
	next := &domain.RuleSet{Slug: "rev", Name: "Rev", Enabled: true, Content: "- MATCH,REJECT"}
	if err := repo.Save(ctx, next); err != nil {
		t.Fatal(err)
	}
	after, _ := repo.GetBySlug(ctx, "rev")
	if after.Revision == before.Revision {
		t.Fatal("revision did not change after save")
	}
	if next.Revision != after.Revision {
		t.Fatalf("Save reported %q, file has %q", next.Revision, after.Revision)
	}
}

func TestRuleSetStaleSaveDetectedAfterDirectWriteWithSameMtimeAndSize(t *testing.T) {
	ctx := context.Background()
	repo, path := newRevisionRepo(t)
	before, _ := repo.GetBySlug(ctx, "rev") // primes the cache
	st, _ := os.Stat(path)
	raw, _ := os.ReadFile(path)
	edited := []byte(strings.Replace(string(raw), "DIRECT", "REJECT", 1))
	if len(edited) != len(raw) { // DIRECT and REJECT are both six bytes
		t.Fatal("test edit must keep the size identical")
	}
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	// The read cache keys on (mtime, size) and cannot see this edit, but a
	// compare-and-write always hashes the live bytes, so a save carrying the
	// revision the reader saw is still refused.
	err := repo.SaveIfRevision(ctx, &domain.RuleSet{Slug: "rev", Name: "Rev", Content: "x"}, before.Revision)
	if !errors.Is(err, domain.ErrStaleRevision) {
		t.Fatalf("want ErrStaleRevision, got %v", err)
	}
	// The refusal drops the stale cache entry, so the next read is fresh.
	after, _ := repo.GetBySlug(ctx, "rev")
	if after.Revision == before.Revision {
		t.Fatal("revision not refreshed after on-disk edit")
	}
}

func TestRuleSetSaveIfRevisionMatchAndMismatch(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRevisionRepo(t)
	cur, _ := repo.GetBySlug(ctx, "rev")

	stale := &domain.RuleSet{Slug: "rev", Name: "Stale", Content: "- MATCH,REJECT"}
	if err := repo.SaveIfRevision(ctx, stale, "0000000000000000"); !errors.Is(err, domain.ErrStaleRevision) {
		t.Fatalf("want stale, got %v", err)
	}
	if got, _ := repo.GetBySlug(ctx, "rev"); got.Name != "Rev" || got.Revision != cur.Revision {
		t.Fatalf("stale save modified the file: %#v", got)
	}

	ok := &domain.RuleSet{Slug: "rev", Name: "Fresh", Content: "- MATCH,REJECT"}
	if err := repo.SaveIfRevision(ctx, ok, cur.Revision); err != nil {
		t.Fatal(err)
	}
	if ok.Revision == "" || ok.Revision == cur.Revision {
		t.Fatalf("new revision not reported: %q", ok.Revision)
	}
}

func TestRuleSetSaveIfRevisionMissingFileIsNotFound(t *testing.T) {
	repo, path := newRevisionRepo(t)
	cur, _ := repo.GetBySlug(context.Background(), "rev")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	err := repo.SaveIfRevision(context.Background(), &domain.RuleSet{Slug: "rev", Name: "x"}, cur.Revision)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound (same as GetBySlug), got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file was recreated")
	}
}

func TestRuleSetSaveIfRevisionConcurrentWritersExactlyOneWins(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRevisionRepo(t)
	base, _ := repo.GetBySlug(ctx, "rev")
	const writers = 8
	var wins, stales atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rs := &domain.RuleSet{Slug: "rev", Name: fmt.Sprintf("writer-%d", i), Content: "- MATCH,DIRECT"}
			switch err := repo.SaveIfRevision(ctx, rs, base.Revision); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, domain.ErrStaleRevision):
				stales.Add(1)
			default:
				t.Errorf("writer %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || stales.Load() != writers-1 {
		t.Fatalf("wins=%d stales=%d", wins.Load(), stales.Load())
	}
}
