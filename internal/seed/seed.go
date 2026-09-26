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
	"slices"

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
	if err := upgradeUnmodifiedRoutingDefaults(configDir); err != nil {
		return err
	}
	return upgradeUnmodifiedDNSDefaults(configDir)
}

type managedDefaultUpdate struct {
	relPath    string
	oldSHA256s []string
	newBody    []byte
}

// upgradeUnmodifiedRoutingDefaults upgrades known official routing defaults to
// the current independent QUIC/UDP selectors. The two files form one bundle: if either
// contains administrator edits, neither is changed, avoiding a half-upgraded
// routing policy. A file already at the new embedded version is also accepted,
// so an interrupted two-file update completes on the next boot.
func upgradeUnmodifiedRoutingDefaults(configDir string) error {
	updates := []managedDefaultUpdate{
		{relPath: "templates/default-mihomo.yaml", oldSHA256s: []string{
			"13cd9b7b8d29447f86fd46503536e15359e07116c302d3b5364a66e879a84c3c",
		}},
		{relPath: "rulesets/default-rules.yaml", oldSHA256s: []string{
			"01c4be93d1bb183336940faa8ed8ebf0f08110adee12327405ab659be282adbc",
			// Independent selectors with QUIC displayed before UDP, through v4.0.0-beta.7.
			"81ca6e2e15c700478b8a15b59ef006f4f2b46043b587484b6b6238b7dee039c3",
			// UDP then QUIC display order, with both selectors defaulting DIRECT.
			"caf6d32b70f2ae4a66b5879e409280caa3552a1121a8bc137164e1c32efa112d",
			// UDP defaults PASS and QUIC defaults DIRECT, through v4.0.0-beta.10.
			"1eab50bef8b214380cceaabacf89d1a4ec5b223193b853a53466274fac73fbe2",
			// UDP defaults PASS and QUIC defaults REJECT, until UDP returned to DIRECT
			// with QUIC following it.
			"e97534ef1ca29916168f330ce70e422ceeecdd2c43b0be5aa76766d8608cd830",
		}},
	}
	for i := range updates {
		body, err := defaultsFS.ReadFile("files/" + updates[i].relPath)
		if err != nil {
			return fmt.Errorf("read managed default files/%s: %w", updates[i].relPath, err)
		}
		updates[i].newBody = body
	}
	return upgradeManagedDefaults(configDir, updates)
}

// upgradeUnmodifiedDNSDefaults updates each untouched template independently,
// so administrator edits to one template do not hold back the other.
func upgradeUnmodifiedDNSDefaults(configDir string) error {
	for _, update := range []managedDefaultUpdate{
		{relPath: "templates/default-mihomo.yaml", oldSHA256s: []string{
			// Official LF and CRLF checkouts of the previous default.
			"f7e3a2784a67fcdf38ba580f51ba0ae577aac4bcfc1c72efc32c8c39ca4f3e4b",
			"426353853592fb9a17afe5cbe801c8c674fc9394ad052b45b3b6ea20789b093d",
			// The Gateway hostname update before selector-aware DNS routing.
			"fbe93907df14dcfe5a6d26207c7fbd5b717be1734491bbeb5aa6de66d636af47",
			"802667bf6c7caf954dc431bbd926b045583333a570320172a082621a6a635038",
		}},
		{relPath: "templates/default-sing-box.yaml", oldSHA256s: []string{
			"e73031f8844a9bc54922510ff209286f3fb24f709de448427fb32f0ebef5cf27",
			"6cfb82005a288b1b2a662cb91dd249337f0678b3ff0df4a10551b25f735439f8",
		}},
	} {
		body, err := defaultsFS.ReadFile("files/" + update.relPath)
		if err != nil {
			return fmt.Errorf("read managed default files/%s: %w", update.relPath, err)
		}
		update.newBody = body
		if err := upgradeManagedDefaults(configDir, []managedDefaultUpdate{update}); err != nil {
			return err
		}
	}
	return nil
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
		switch {
		case currentHash == newHash:
			// Already upgraded (or newly created by Ensure).
		case slices.Contains(update.oldSHA256s, currentHash):
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
