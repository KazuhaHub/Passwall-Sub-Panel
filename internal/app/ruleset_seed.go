package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type ruleSetSeedFile struct {
	Slug    string `yaml:"slug"`
	Name    string `yaml:"name"`
	Sort    int    `yaml:"sort"`
	Enabled bool   `yaml:"enabled"`
	Content string `yaml:"content"`
}

func seedRuleSetsIfEmpty(ctx context.Context, configDir string, repo ports.RuleSetRepo) error {
	existing, err := repo.List(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}

	dir := filepath.Join(configDir, "rulesets")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	seeded := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc ruleSetSeedFile
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return err
		}
		doc.Slug = strings.TrimSpace(doc.Slug)
		doc.Name = strings.TrimSpace(doc.Name)
		doc.Content = strings.TrimRight(doc.Content, "\n")
		if doc.Slug == "" || doc.Name == "" || doc.Content == "" {
			continue
		}
		if err := repo.Save(ctx, &domain.RuleSet{
			Slug:    doc.Slug,
			Name:    doc.Name,
			Sort:    doc.Sort,
			Enabled: doc.Enabled,
			Content: doc.Content,
		}); err != nil {
			return err
		}
		seeded++
	}
	if seeded > 0 {
		log.Info("seeded rule sets", "count", seeded, "dir", dir)
	}
	return nil
}
