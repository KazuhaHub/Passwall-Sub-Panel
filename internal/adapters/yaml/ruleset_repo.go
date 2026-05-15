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
	doc := ruleSetFile{
		Slug:            rs.Slug,
		Name:            rs.Name,
		Sort:            rs.Sort,
		Enabled:         rs.Enabled,
		ProxyGroupOrder: rs.ProxyGroupOrder,
		Content:         rs.Content,
	}
	return writeYAML(r.pathOf(rs.Slug), doc)
}

func (r *RuleSetRepo) Delete(ctx context.Context, slug string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.pathForSlug(slug)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func (r *RuleSetRepo) pathOf(slug string) string {
	return filepath.Join(r.dir, slug+".yaml")
}

func (r *RuleSetRepo) pathForSlug(slug string) (string, error) {
	p := r.pathOf(slug)
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
