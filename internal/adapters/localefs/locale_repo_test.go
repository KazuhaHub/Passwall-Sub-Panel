package localefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func newPack(code, name string) *domain.LocalePack {
	return &domain.LocalePack{
		Format:       1,
		Code:         code,
		Name:         name,
		Author:       "tester",
		BaseLanguage: "en-US",
		BaseVersion:  "3.9.0",
		Namespaces: map[string]map[string]any{
			"common": {"app": map[string]any{"save": name + "-save"}},
			"nav":    {"home": name + "-home"},
		},
	}
}

func TestLocaleRepoSaveListGetDelete(t *testing.T) {
	ctx := context.Background()
	repo, err := NewLocaleRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.Save(ctx, newPack("fr-FR", "Français")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, newPack("de-DE", "Deutsch")); err != nil {
		t.Fatal(err)
	}

	metas, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by code for a stable switcher order.
	if len(metas) != 2 || metas[0].Code != "de-DE" || metas[1].Code != "fr-FR" {
		t.Fatalf("unexpected list: %#v", metas)
	}
	if metas[1].Name != "Français" || metas[1].ETag == "" {
		t.Fatalf("meta missing name/etag: %#v", metas[1])
	}

	got, err := repo.Get(ctx, "fr-FR")
	if err != nil {
		t.Fatal(err)
	}
	nav, ok := got.Namespaces["nav"]
	if !ok || nav["home"] != "Français-home" {
		t.Fatalf("unexpected pack round-trip: %#v", got.Namespaces)
	}

	if _, err := os.Stat(filepath.Join(repo.dir, "fr-FR.json")); err != nil {
		t.Fatalf("expected pack file: %v", err)
	}

	if err := repo.Delete(ctx, "fr-FR"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, "fr-FR"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get deleted err = %v, want ErrNotFound", err)
	}
}

func TestLocaleRepoGetUnknown(t *testing.T) {
	ctx := context.Background()
	repo, err := NewLocaleRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, "xx-YY"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get missing err = %v, want ErrNotFound", err)
	}
}

// TestLocaleRepoPicksUpExternalEdit locks the mtime-cache behaviour: an admin
// editing the JSON file on disk (outside Save) must be reflected on the next Get.
func TestLocaleRepoPicksUpExternalEdit(t *testing.T) {
	ctx := context.Background()
	repo, err := NewLocaleRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, newPack("fr-FR", "Français")); err != nil {
		t.Fatal(err)
	}
	// Prime the cache.
	if _, err := repo.Get(ctx, "fr-FR"); err != nil {
		t.Fatal(err)
	}

	// Overwrite the file directly with new content and a distinct mtime.
	path := filepath.Join(repo.dir, "fr-FR.json")
	raw := []byte(`{"psp_language_pack":1,"code":"fr-FR","name":"Français (edited)","namespaces":{"nav":{"home":"Accueil-edited"}}}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, "fr-FR")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Français (edited)" || got.Namespaces["nav"]["home"] != "Accueil-edited" {
		t.Fatalf("external edit not picked up: %#v", got)
	}
}

func TestLocaleRepoRejectsUnsafeCode(t *testing.T) {
	ctx := context.Background()
	repo, err := NewLocaleRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"../escape", "a/b", ""} {
		p := newPack(code, "X")
		if err := repo.Save(ctx, p); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("Save unsafe code %q err = %v, want ErrValidation", code, err)
		}
	}
}
