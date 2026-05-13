package yaml

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// RuleSetRepo implements ports.RuleSetRepo. Each shared rule set is one YAML
// file under config/rulesets/.
type RuleSetRepo struct {
	dir string
	mu  sync.RWMutex
}

func NewRuleSetRepo(configDir string) (*RuleSetRepo, error) {
	dir := filepath.Join(configDir, "rulesets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &RuleSetRepo{dir: dir}, nil
}

type ruleSetFile struct {
	Slug    string `yaml:"slug"`
	Name    string `yaml:"name"`
	Sort    int    `yaml:"sort"`
	Enabled bool   `yaml:"enabled"`
	Content string `yaml:"content"`
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

func (r *RuleSetRepo) GetBySlug(ctx context.Context, slug string) (*domain.RuleSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p := r.pathOf(slug)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return nil, domain.ErrNotFound
		}
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
	doc := ruleSetFile{
		Slug:    rs.Slug,
		Name:    rs.Name,
		Sort:    rs.Sort,
		Enabled: rs.Enabled,
		Content: rs.Content,
	}
	return writeYAML(r.pathOf(rs.Slug), doc)
}

func (r *RuleSetRepo) Delete(ctx context.Context, slug string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return os.Remove(r.pathOf(slug))
}

func (r *RuleSetRepo) pathOf(slug string) string {
	return filepath.Join(r.dir, slug+".yaml")
}

func (r *RuleSetRepo) readFile(path string) (*domain.RuleSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc ruleSetFile
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	return &domain.RuleSet{
		Slug:    doc.Slug,
		Name:    doc.Name,
		Sort:    doc.Sort,
		Enabled: doc.Enabled,
		Content: doc.Content,
	}, nil
}
