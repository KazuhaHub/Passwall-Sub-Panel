package seed

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeManagedDefaultsUpdatesOnlyTheOfficialBundle(t *testing.T) {
	oldTemplate := []byte("old template\n")
	oldRules := []byte("old rules\n")
	newTemplate := []byte("new template\n")
	newRules := []byte("new rules\n")
	updates := []managedDefaultUpdate{
		{relPath: "templates/default.yaml", oldSHA256: testSHA256(oldTemplate), newBody: newTemplate},
		{relPath: "rulesets/default.yaml", oldSHA256: testSHA256(oldRules), newBody: newRules},
	}

	t.Run("both official defaults", func(t *testing.T) {
		dir := writeManagedTestFiles(t, oldTemplate, oldRules)
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
	checks := map[string]string{
		"templates/default-mihomo.yaml": "13cd9b7b8d29447f86fd46503536e15359e07116c302d3b5364a66e879a84c3c",
		"rulesets/default-rules.yaml":   "01c4be93d1bb183336940faa8ed8ebf0f08110adee12327405ab659be282adbc",
	}
	for relPath, oldHash := range checks {
		body, err := defaultsFS.ReadFile("files/" + relPath)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		if testSHA256(body) == oldHash {
			t.Fatalf("managed default %s still equals its legacy hash", relPath)
		}
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
