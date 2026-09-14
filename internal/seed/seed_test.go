package seed

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeManagedDefaultsUpdatesOnlyTheOfficialBundle(t *testing.T) {
	oldTemplate := []byte("old template\n")
	oldRules := []byte("old rules\n")
	olderRules := []byte("older rules\n")
	newTemplate := []byte("new template\n")
	newRules := []byte("new rules\n")
	updates := []managedDefaultUpdate{
		{relPath: "templates/default.yaml", oldSHA256s: []string{testSHA256(oldTemplate)}, newBody: newTemplate},
		{relPath: "rulesets/default.yaml", oldSHA256s: []string{testSHA256(olderRules), testSHA256(oldRules)}, newBody: newRules},
	}

	t.Run("both official defaults", func(t *testing.T) {
		dir := writeManagedTestFiles(t, oldTemplate, oldRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", newTemplate)
		assertManagedTestFile(t, dir, "rulesets/default.yaml", newRules)
	})

	t.Run("earlier official defaults remain supported", func(t *testing.T) {
		dir := writeManagedTestFiles(t, oldTemplate, olderRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", newTemplate)
		assertManagedTestFile(t, dir, "rulesets/default.yaml", newRules)
	})

	t.Run("one customized file preserves the whole bundle", func(t *testing.T) {
		dir := writeManagedTestFiles(t, []byte("administrator edit\n"), oldRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", []byte("administrator edit\n"))
		assertManagedTestFile(t, dir, "rulesets/default.yaml", oldRules)
	})

	t.Run("interrupted update completes", func(t *testing.T) {
		dir := writeManagedTestFiles(t, newTemplate, oldRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", newTemplate)
		assertManagedTestFile(t, dir, "rulesets/default.yaml", newRules)
	})
}

func TestRoutingDefaultMigrationSourcesExistAndChanged(t *testing.T) {
	checks := map[string][]string{
		"templates/default-mihomo.yaml": {"13cd9b7b8d29447f86fd46503536e15359e07116c302d3b5364a66e879a84c3c"},
		"rulesets/default-rules.yaml": {
			"01c4be93d1bb183336940faa8ed8ebf0f08110adee12327405ab659be282adbc",
			"81ca6e2e15c700478b8a15b59ef006f4f2b46043b587484b6b6238b7dee039c3",
		},
	}
	for relPath, oldHashes := range checks {
		body, err := defaultsFS.ReadFile("files/" + relPath)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		for _, oldHash := range oldHashes {
			if testSHA256(body) == oldHash {
				t.Fatalf("managed default %s still equals its legacy hash", relPath)
			}
		}
	}
}

func TestEnsureUpgradesPreviousIndependentRoutingDefaults(t *testing.T) {
	template, err := defaultsFS.ReadFile("files/templates/default-mihomo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// This template did not change; pin its published beta.7 bytes rather than
	// accidentally testing a newly generated template as the previous version.
	if got := testSHA256(template); got != "d83f169df2cd5f5889c5635c074f0546db46c4f7e319e818b80445b9ee8a6dd0" {
		t.Fatalf("previous official template hash = %s", got)
	}
	rules, err := defaultsFS.ReadFile("files/rulesets/default-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the prior official prefix without duplicating the unchanged
	// 9,000-line ruleset. The independently recorded published hash below proves
	// the reconstructed bytes really are beta.7, not the current default.
	previousRules := []byte(strings.NewReplacer(
		"  - '🎮 UDP控制'\n  - '⚡ QUIC控制'\n", "  - '⚡ QUIC控制'\n  - '🎮 UDP控制'\n",
		"to the general UDP selector (DIRECT), but", "to the general UDP selector, but",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector, default DIRECT.\n"+
			"  # UDP stays enabled and uses the local exit IP even when the main node is a proxy.\n"+
			"  # Group display order is UDP then QUIC; matching must remain QUIC then UDP.\n"+
			"  # Both rules\n",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector. Both rules\n",
	).Replace(string(rules)))
	if got := testSHA256(previousRules); got != "81ca6e2e15c700478b8a15b59ef006f4f2b46043b587484b6b6238b7dee039c3" {
		t.Fatalf("previous official rules hash = %s", got)
	}
	for _, tc := range []struct {
		name              string
		customizeTemplate bool
		customizeRules    bool
	}{
		{name: "untouched beta.7 defaults upgrade"},
		{name: "customized template preserves whole bundle", customizeTemplate: true},
		{name: "customized rules preserve whole bundle", customizeRules: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			oldTemplate := append([]byte(nil), template...)
			oldRules := append([]byte(nil), previousRules...)
			if tc.customizeTemplate {
				oldTemplate = append(oldTemplate, []byte("# administrator customization\n")...)
			}
			if tc.customizeRules {
				oldRules = append(oldRules, []byte("# administrator customization\n")...)
			}
			for relPath, body := range map[string][]byte{
				"templates/default-mihomo.yaml": oldTemplate,
				"rulesets/default-rules.yaml":   oldRules,
			} {
				path := filepath.Join(dir, relPath)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			wantRules := rules
			if tc.customizeTemplate || tc.customizeRules {
				wantRules = oldRules
			}
			// Ensure must be idempotent for both upgraded and customized bundles.
			for attempt := 0; attempt < 2; attempt++ {
				if err := Ensure(dir); err != nil {
					t.Fatal(err)
				}
				assertManagedTestFile(t, dir, "templates/default-mihomo.yaml", oldTemplate)
				assertManagedTestFile(t, dir, "rulesets/default-rules.yaml", wantRules)
			}
		})
	}
}

func writeManagedTestFiles(t *testing.T, template, rules []byte) string {
	t.Helper()
	dir := t.TempDir()
	for relPath, body := range map[string][]byte{
		"templates/default.yaml": template,
		"rulesets/default.yaml":  rules,
	} {
		path := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func assertManagedTestFile(t *testing.T, dir, relPath string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s = %q, want %q", relPath, got, want)
	}
}

func testSHA256(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}
