package yaml

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gopkg.in/yaml.v3"
)

var safeSlugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func pathForSafeSlug(dir, slug, kind string) (string, error) {
	if slug == "" {
		return "", fmt.Errorf("%w: %s slug empty", domain.ErrValidation, kind)
	}
	if !safeSlugPattern.MatchString(slug) {
		return "", fmt.Errorf("%w: %s slug may only contain letters, numbers, '_' and '-'", domain.ErrValidation, kind)
	}
	p := filepath.Join(dir, slug+".yaml")
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %s path escapes config directory", domain.ErrValidation, kind)
	}
	return p, nil
}

// writeYAML writes a YAML file atomically: write to .tmp then rename.
func writeYAML(path string, v any) error {
	_, err := writeYAMLBytes(path, v)
	return err
}

// writeYAMLBytes is writeYAML that also returns the exact bytes written, so a
// caller can hash what is now on disk without a re-read.
func writeYAMLBytes(path string, v any) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return b, nil
}
