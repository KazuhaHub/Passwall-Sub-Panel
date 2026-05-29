package yaml

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// RuleSetRepo implements ports.RuleSetRepo. Each shared rule set is one YAML
// file under config/rulesets/.
//
// mtime cache: same rationale as TemplateRepo — /sub render walks every
// referenced ruleset, re-parsing YAML on each request was a measurable
// dominant cost at polling-fleet scale.
type RuleSetRepo struct {
	dir   string
	mu    sync.RWMutex
	cache sync.Map // map[string]ruleSetCacheEntry — key = absolute file path
}

type ruleSetCacheEntry struct {
	mtime time.Time
	value *domain.RuleSet
}

func NewRuleSetRepo(configDir string) (*RuleSetRepo, error) {
	dir := filepath.Join(configDir, "rulesets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &RuleSetRepo{dir: dir}, nil
}

type ruleSetFile struct {
	Slug            string   `yaml:"slug"`
	Name            string   `yaml:"name"`
	Sort            int      `yaml:"sort"`
	Enabled         bool     `yaml:"enabled"`
	ProxyGroupOrder []string `yaml:"proxy_group_order"`
	Content         string   `yaml:"content"`
}

func (r *RuleSetRepo) List(ctx context.Context) ([]*domain.RuleSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, err
	}
	out := []*domain.RuleSet{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		rs, err := r.readFile(filepath.Join(r.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out = append(out, rs)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

func (r *RuleSetRepo) ListPaged(ctx context.Context, p ports.Pagination) ([]*domain.RuleSet, int64, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, 0, err
	}
	// Filter by keyword (slug / name).
	filtered := all[:0]
	for _, rs := range all {
		if keywordMatch(p.Keyword, rs.Slug, rs.Name) {
			filtered = append(filtered, rs)
		}
	}
	// Sort. Default "sort" mirrors the List() default ordering.
	switch p.SortBy {
	case "slug":
		sortBy(filtered, p.SortDir, func(a, b *domain.RuleSet) bool { return a.Slug < b.Slug })
	case "name":
		sortBy(filtered, p.SortDir, func(a, b *domain.RuleSet) bool { return a.Name < b.Name })
	default: // "sort" or unrecognized
		sortBy(filtered, p.SortDir, func(a, b *domain.RuleSet) bool {
			if a.Sort != b.Sort {
				return a.Sort < b.Sort
			}
			return a.Slug < b.Slug
		})
	}
	page, total := slicePage(filtered, p)
	return page, total, nil
}

func (r *RuleSetRepo) GetBySlug(ctx context.Context, slug string) (*domain.RuleSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, err := r.pathForSlug(slug)
	if err != nil {
		return nil, err
	}
	return r.readFile(p)
}

func (r *RuleSetRepo) Save(ctx context.Context, rs *domain.RuleSet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rs.Slug == "" {
		return fmt.Errorf("%w: rule set slug empty", domain.ErrValidation)
	}
	// Resolve the target file the SAME way GetBySlug/Delete do (pathForSlug):
	// an existing rule set with this slug is overwritten in place even when its
	// filename differs from the slug. The seeded default ships as
	// default-rules.yaml but carries slug "default_rules"; resolving by slug
	// alone (pathOf -> default_rules.yaml) wrote a SECOND file next to the seed,
	// so the list surfaced two rows sharing slug "default_rules". One slug now
	// maps to exactly one file. ErrNotFound = genuinely new slug -> create
	// <slug>.yaml via pathOf.
	p, err := r.pathForSlug(rs.Slug)
	if errors.Is(err, domain.ErrNotFound) {
		p, err = r.pathOf(rs.Slug)
	}
	if err != nil {
		return err
	}
	doc := ruleSetFile{
		Slug:            rs.Slug,
		Name:            rs.Name,
		Sort:            rs.Sort,
		Enabled:         rs.Enabled,
		ProxyGroupOrder: rs.ProxyGroupOrder,
		Content:         rs.Content,
	}
	if err := writeYAML(p, doc); err != nil {
		return err
	}
	r.cache.Delete(p)
	return nil
}

func (r *RuleSetRepo) Delete(ctx context.Context, slug string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.pathForSlug(slug)
	if err != nil {
		return err
	}
	r.cache.Delete(p)
	return os.Remove(p)
}

func (r *RuleSetRepo) pathOf(slug string) (string, error) {
	return pathForSafeSlug(r.dir, slug, "rule set")
}

func (r *RuleSetRepo) pathForSlug(slug string) (string, error) {
	p, err := r.pathOf(slug)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return p, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		candidate := filepath.Join(r.dir, e.Name())
		rs, err := r.readFile(candidate)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if rs.Slug == slug {
			return candidate, nil
		}
	}
	return "", domain.ErrNotFound
}

func (r *RuleSetRepo) readFile(path string) (*domain.RuleSet, error) {
	if st, err := os.Stat(path); err == nil {
		if v, ok := r.cache.Load(path); ok {
			entry := v.(ruleSetCacheEntry)
			if entry.mtime.Equal(st.ModTime()) {
				return entry.value, nil
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc ruleSetFile
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, err
		}
		rs := &domain.RuleSet{
			Slug:            doc.Slug,
			Name:            doc.Name,
			Sort:            doc.Sort,
			Enabled:         doc.Enabled,
			ProxyGroupOrder: doc.ProxyGroupOrder,
			Content:         doc.Content,
		}
		r.cache.Store(path, ruleSetCacheEntry{mtime: st.ModTime(), value: rs})
		return rs, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc ruleSetFile
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	return &domain.RuleSet{
		Slug:            doc.Slug,
		Name:            doc.Name,
		Sort:            doc.Sort,
		Enabled:         doc.Enabled,
		ProxyGroupOrder: doc.ProxyGroupOrder,
		Content:         doc.Content,
	}, nil
}
