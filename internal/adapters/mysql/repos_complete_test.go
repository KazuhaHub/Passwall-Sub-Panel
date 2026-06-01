package mysql

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestNewReposPopulatesEveryDBRepo guards the drift that broke v3.6.3-beta.5:
// a new repo (AuthEvent) was added to ports.Repos but a field-by-field copy in
// app.go dropped it → nil interface → handler panic. app.go now derives its
// Repos from NewRepos (`repos := mysqlRepos`), so as long as NewRepos wires
// every DB-backed field, the derivation carries it. This asserts exactly that:
// every Repos field is non-nil EXCEPT the two YAML-backed repos that app.go
// intentionally injects (rule sets / templates live in config/*.yaml).
func TestNewReposPopulatesEveryDBRepo(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// YAML-backed repos are wired by app.go (not the DB), so NewRepos leaves
	// them nil on purpose.
	yamlBacked := map[string]bool{"RuleSet": true, "Template": true}

	repos := NewRepos(db)
	v := reflect.ValueOf(repos)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if yamlBacked[name] {
			continue
		}
		if v.Field(i).IsNil() {
			t.Errorf("NewRepos left ports.Repos.%s nil — every DB-backed repo must be wired here "+
				"so app.go's `repos := mysqlRepos` derivation carries it (a nil repo panics the handler)", name)
		}
	}
}
