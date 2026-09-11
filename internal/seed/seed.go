// Package seed releases default rulesets and templates into the runtime
// config directory on first launch. The defaults are embedded into the
// binary, so the panel can boot from an empty config dir whether it lives
// on a freshly bind-mounted Docker volume or a clean systemd /opt/psp path.
//
// Existing files in the config dir are never overwritten unless every file in
// a versioned migration bundle is byte-for-byte identical to a known official
// default. This lets untouched installations receive correctness fixes while
// preserving any administrator customization.
package seed

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// The "all:" prefix includes dotfiles, so hidden default fragments (if any
// are added later) are still picked up.
//
//go:embed all:files
var defaultsFS embed.FS

// RestoreBySlug walks the embedded files under files/<subdir>/, finds
// the YAML whose `slug:` field matches the requested slug, and writes
// its contents back to <configDir>/<subdir>/<embed-basename>. The
// embed basename is used (not "<slug>.yaml") because seed file names
// and slug fields don't have to match — default-rules.yaml carries
// slug: default_rules — and we want to overwrite the same file the
// yaml repo's pathForSlug already discovers.
//
// Used by the admin "reset to default" affordance for templates and
// rulesets. Returns ErrSeedNotFound when no embedded YAML in that
// subdir carries the requested slug so callers can map it to a 404.
func RestoreBySlug(configDir, subdir, slug string) error {
	body, basename, err := findEmbedBySlug(subdir, slug)
	if err != nil {
		return err
	}
	targetDir := filepath.Join(configDir, subdir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", targetDir, err)
	}
	target := filepath.Join(targetDir, basename)
	if err := os.WriteFile(target, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}

// HasSeededSlug reports whether the binary carries an embedded YAML in
// files/<subdir>/ whose slug: field matches the requested slug. Used by
// admin write paths (Delete) to refuse mutations that would orphan a
// canonical default — the Reset button only works as a recovery when
// the slug still exists.
func HasSeededSlug(subdir, slug string) bool {
	_, _, err := findEmbedBySlug(subdir, slug)
	return err == nil
}

// findEmbedBySlug returns (file body, file basename) for the entry in
// files/<subdir>/ whose YAML `slug:` matches. ErrSeedNotFound when no
// match is found.
func findEmbedBySlug(subdir, slug string) ([]byte, string, error) {
	entries, err := defaultsFS.ReadDir("files/" + subdir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", ErrSeedNotFound
		}
		return nil, "", fmt.Errorf("read embed dir files/%s: %w", subdir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		embedPath := "files/" + subdir + "/" + e.Name()
		body, err := defaultsFS.ReadFile(embedPath)
		if err != nil {
			return nil, "", fmt.Errorf("read embed %s: %w", embedPath, err)
		}
		var head struct {
			Slug string `yaml:"slug"`
		}
		if err := yaml.Unmarshal(body, &head); err != nil {
			continue
		}
		if head.Slug == slug {
			return body, e.Name(), nil
		}
	}
	return nil, "", ErrSeedNotFound
}

// ErrSeedNotFound is returned by Restore when relPath isn't carried in
// the binary. Sentinel so HTTP handlers can map it to 404.
var ErrSeedNotFound = errors.New("seed: no embedded default for this slug")

// Ensure walks the baked-in defaults and writes any file that is missing
// under configDir. Existing files are preserved except for explicit,
// hash-gated migrations of byte-identical former official defaults.
func Ensure(configDir string) error {
	if err := fs.WalkDir(defaultsFS, "files", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "files" {
			return nil
		}
		rel := path[len("files/"):]
		target := filepath.Join(configDir, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if _, err := os.Stat(target); err == nil {
			return nil // already present — preserve admin edits
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", target, err)
		}

		body, err := defaultsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	}); err != nil {
		return err
	}
	return upgradeUnmodifiedRoutingDefaults(configDir)
}

type managedDefaultUpdate struct {
	relPath   string
	oldSHA256 string
	newBody   []byte
}

// upgradeUnmodifiedRoutingDefaults moves the original QUIC-reject defaults to
// the independent QUIC/UDP selectors. The two files form one bundle: if either
// contains administrator edits, neither is changed, avoiding a half-upgraded
// routing policy. A file already at the new embedded version is also accepted,
// so an interrupted two-file update completes on the next boot.
func upgradeUnmodifiedRoutingDefaults(configDir string) error {
	specs := []struct {
		relPath   string
		oldSHA256 string
	}{
		{"templates/default-mihomo.yaml", "13cd9b7b8d29447f86fd46503536e15359e07116c302d3b5364a66e879a84c3c"},
		{"rulesets/default-rules.yaml", "01c4be93d1bb183336940faa8ed8ebf0f08110adee12327405ab659be282adbc"},
	}
	updates := make([]managedDefaultUpdate, 0, len(specs))
	for _, spec := range specs {
		body, err := defaultsFS.ReadFile("files/" + spec.relPath)
		if err != nil {
			return fmt.Errorf("read managed default files/%s: %w", spec.relPath, err)
		}
		updates = append(updates, managedDefaultUpdate{relPath: spec.relPath, oldSHA256: spec.oldSHA256, newBody: body})
	}
	return upgradeManagedDefaults(configDir, updates)
}

func upgradeManagedDefaults(configDir string, updates []managedDefaultUpdate) error {
	needsWrite := make([]bool, len(updates))
	for i, update := range updates {
		body, err := os.ReadFile(filepath.Join(configDir, update.relPath))
		if err != nil {
			return fmt.Errorf("read managed default %s: %w", update.relPath, err)
		}
		currentHash := fmt.Sprintf("%x", sha256.Sum256(body))
		newHash := fmt.Sprintf("%x", sha256.Sum256(update.newBody))
		switch currentHash {
		case newHash:
			// Already upgraded (or newly created by Ensure).
		case update.oldSHA256:
			needsWrite[i] = true
		default:
			return nil // bundle contains an administrator customization
		}
	}
	for i, update := range updates {
		if !needsWrite[i] {
			continue
		}
		target := filepath.Join(configDir, update.relPath)
		if err := os.WriteFile(target, update.newBody, 0o644); err != nil {
			return fmt.Errorf("upgrade managed default %s: %w", update.relPath, err)
		}
	}
	return nil
}
