// Package localefs implements ports.LocaleRepo as one JSON file per language
// under <ConfigDir>/locales/. It mirrors the mtime-cache pattern of the
// YAML-backed template/ruleset repos so an admin can edit a pack file on disk
// and have the next read pick it up without a restart.
//
// It is a self-contained adapter: per the hexagonal rule it imports only domain
// (never service/locale), so the file-safe code check is duplicated here as a
// small pathForCode. Full structural validation of an uploaded pack lives in
// service/locale.Validate and runs in the HTTP handler before Save is ever called.
package localefs

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

var codePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// LocaleRepo persists uploaded language packs as <dir>/<code>.json.
type LocaleRepo struct {
	dir   string
	mu    sync.RWMutex
	cache sync.Map // key = absolute file path → localeCacheEntry
}

type localeCacheEntry struct {
	mtime time.Time
	value *domain.LocalePack
}

// NewLocaleRepo opens (creating if needed) <configDir>/locales.
func NewLocaleRepo(configDir string) (*LocaleRepo, error) {
	dir := filepath.Join(configDir, "locales")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &LocaleRepo{dir: dir}, nil
}

// localeFile is the on-disk / wire JSON shape. Kept separate from domain so the
// json tags (and the `psp_language_pack` format field) live at the adapter edge.
type localeFile struct {
	Format       int                       `json:"psp_language_pack"`
	Code         string                    `json:"code"`
	Name         string                    `json:"name"`
	Author       string                    `json:"author,omitempty"`
	BaseLanguage string                    `json:"base_language,omitempty"`
	BaseVersion  string                    `json:"base_version,omitempty"`
	Namespaces   map[string]map[string]any `json:"namespaces"`
}

func toDomain(f *localeFile) *domain.LocalePack {
	return &domain.LocalePack{
		Format:       f.Format,
		Code:         f.Code,
		Name:         f.Name,
		Author:       f.Author,
		BaseLanguage: f.BaseLanguage,
		BaseVersion:  f.BaseVersion,
		Namespaces:   f.Namespaces,
	}
}

func fromDomain(p *domain.LocalePack) *localeFile {
	return &localeFile{
		Format:       p.Format,
		Code:         p.Code,
		Name:         p.Name,
		Author:       p.Author,
		BaseLanguage: p.BaseLanguage,
		BaseVersion:  p.BaseVersion,
		Namespaces:   p.Namespaces,
	}
}

// List returns the manifest (no translation bodies) sorted by code.
func (r *LocaleRepo) List(ctx context.Context) ([]domain.LocaleMeta, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, err
	}
	out := []domain.LocaleMeta{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(r.dir, e.Name())
		p, err := r.readFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.LocaleMeta{
			Code:        p.Code,
			Name:        p.Name,
			Author:      p.Author,
			BaseVersion: p.BaseVersion,
			ETag:        etag(st),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

// Get returns one full pack, or domain.ErrNotFound.
func (r *LocaleRepo) Get(ctx context.Context, code string) (*domain.LocalePack, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, err := r.pathOf(code)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return r.readFile(p)
}

// Save writes the pack atomically and invalidates its cache entry. It does NOT
// run structural validation — the handler runs service/locale.Validate first.
func (r *LocaleRepo) Save(ctx context.Context, p *domain.LocalePack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	path, err := r.pathOf(p.Code)
	if err != nil {
		return err
	}
	if err := writeJSON(path, fromDomain(p)); err != nil {
		return err
	}
	r.cache.Delete(path)
	return nil
}

// Delete removes the pack file and its cache entry.
func (r *LocaleRepo) Delete(ctx context.Context, code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	path, err := r.pathOf(code)
	if err != nil {
		return err
	}
	r.cache.Delete(path)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return domain.ErrNotFound
		}
		return err
	}
	return nil
}

func (r *LocaleRepo) pathOf(code string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("%w: language code empty", domain.ErrValidation)
	}
	if !codePattern.MatchString(code) {
		return "", fmt.Errorf("%w: language code may only contain letters, numbers, '_' and '-'", domain.ErrValidation)
	}
	p := filepath.Join(r.dir, code+".json")
	// Defence in depth: the pattern already forbids '/' and '..', but confirm the
	// resolved path stays inside dir (mirrors yaml/util.go pathForSafeSlug).
	absDir, err := filepath.Abs(r.dir)
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
		return "", fmt.Errorf("%w: language path escapes config directory", domain.ErrValidation)
	}
	return p, nil
}

// readFile parses a pack, using the mtime cache to skip re-read + re-parse when
// the file is unchanged. A failed Stat falls through to a direct read so the real
// error surfaces rather than being masked by a cache bypass.
func (r *LocaleRepo) readFile(path string) (*domain.LocalePack, error) {
	if st, err := os.Stat(path); err == nil {
		if v, ok := r.cache.Load(path); ok {
			entry := v.(localeCacheEntry)
			if entry.mtime.Equal(st.ModTime()) {
				return entry.value, nil
			}
		}
		p, err := parseFile(path)
		if err != nil {
			return nil, err
		}
		r.cache.Store(path, localeCacheEntry{mtime: st.ModTime(), value: p})
		return p, nil
	}
	return parseFile(path)
}

func parseFile(path string) (*domain.LocalePack, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f localeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	return toDomain(&f), nil
}

// writeJSON writes a JSON file atomically: write to .tmp then rename.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// etag derives a cheap validator from the file's mtime + size. Good enough for
// conditional GETs — any admin edit changes both.
func etag(st os.FileInfo) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d-%d", st.ModTime().UnixNano(), st.Size())
	return fmt.Sprintf(`"%x"`, h.Sum64())
}
