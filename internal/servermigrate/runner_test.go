package servermigrate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeMigration struct {
	ports.ServerMigrationRepo
	preview    *domain.ServerMigrationPreview
	previewErr error
	applyErr   error
	applies    int
	agent      *domain.NodeAgent
	credential string
}

func (f *fakeMigration) Preview(context.Context, int64, string, bool) (*domain.ServerMigrationPreview, error) {
	return f.preview, f.previewErr
}

func (f *fakeMigration) Apply(_ context.Context, id int64, fingerprint string, a *domain.NodeAgent, raw string) error {
	if id != 12 || fingerprint != f.preview.Fingerprint {
		return domain.ErrConflict
	}
	f.applies++
	f.agent = a
	f.credential = raw
	return f.applyErr
}

func migrationArgs() []string {
	return []string{"--server-id", "12", "--core-version", "26.5.9", "--expected-fingerprint", strings.Repeat("a", 64), "--all-psp-stopped", "--old-xray-stopped", "--managed-only", "--apply"}
}

func TestMigrationCLIConfirmationBeforeOpeningDatabase(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--server-id", "0"}, {"--server-id", "12", "extra"},
		{"--server-id", "12", "--apply"},
		{"--server-id", "12", "--apply", "--all-psp-stopped", "--old-xray-stopped", "--managed-only"},
	} {
		var out bytes.Buffer
		code := run(context.Background(), args, &out, &out, func(context.Context, string) (*connection, error) {
			t.Fatal("invalid arguments opened database")
			return nil, nil
		})
		if code != 2 {
			t.Fatalf("args %v code=%d: %s", args, code, out.String())
		}
	}
}

func TestMigrationCLIDryRunAndApply(t *testing.T) {
	for _, apply := range []bool{false, true} {
		f := &fakeMigration{preview: &domain.ServerMigrationPreview{
			ServerID: 12, ServerName: "existing", CoreVersion: "26.5.9", Fingerprint: strings.Repeat("a", 64),
			CanMigrate: true, NodeCount: 2, ClientCount: 3,
		}}
		args := []string{"--server-id", "12"}
		if apply {
			args = migrationArgs()
		}
		var out bytes.Buffer
		closed := false
		code := run(context.Background(), args, &out, &out, func(context.Context, string) (*connection, error) {
			return &connection{preview: f, repo: f, close: func() { closed = true }}, nil
		})
		if code != 0 || !closed {
			t.Fatalf("code=%d closed=%v: %s", code, closed, out.String())
		}
		if !apply && f.applies != 0 {
			t.Fatal("dry run changed database")
		}
		if apply && (f.applies != 1 || f.agent.PanelID != 12 || f.agent.DesiredCoreVersion != "26.5.9" || !strings.HasPrefix(f.credential, "pspn_")) {
			t.Fatalf("apply did not retain server identity: %+v", f.agent)
		}
		if f.credential != "" && strings.Contains(out.String(), f.credential) {
			t.Fatal("CLI exposed fixed installation credential")
		}
	}
}

func TestMigrationCLIBoundsAndSanitizesErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeMigration)
	}{
		{"blocker", func(f *fakeMigration) {
			f.preview.Blockers = []domain.MigrationIssue{{Code: "external_files", NodeID: 7}}
		}},
		{"cannot_migrate", func(f *fakeMigration) { f.preview.CanMigrate = false }},
		{"stale", func(f *fakeMigration) { f.preview.Fingerprint = strings.Repeat("b", 64) }},
		{"preview_error", func(f *fakeMigration) { f.previewErr = errors.New("SECRET credential in SQL") }},
		{"apply_error", func(f *fakeMigration) { f.applyErr = errors.New("SECRET credential in SQL") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := &fakeMigration{preview: &domain.ServerMigrationPreview{ServerID: 12, CoreVersion: "26.5.9", Fingerprint: strings.Repeat("a", 64), CanMigrate: true}}
			test.mutate(f)
			var out bytes.Buffer
			code := run(context.Background(), migrationArgs(), &out, &out, func(context.Context, string) (*connection, error) { return &connection{preview: f, repo: f}, nil })
			if code != 1 || strings.Contains(out.String(), "SECRET") {
				t.Fatalf("unsafe failed operation: code=%d %s", code, out.String())
			}
			if test.name != "apply_error" && f.applies != 0 {
				t.Fatal("blocked preview applied conversion")
			}
		})
	}
}

func TestMigrationCLIDoesNotInitializeMissingConfigOrSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.yaml")
	if _, err := openConfiguredDatabase(context.Background(), path); err == nil {
		t.Fatal("missing config accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("maintenance generated config")
	}
	path = filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "missing.db")
	if err := os.WriteFile(path, []byte("jwt_secret: test-only-key-material\nmysql:\n  dsn: sqlite:"+dbPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PSP_MYSQL_DSN", "")
	t.Setenv("PSP_POSTGRES_DSN", "")
	if _, err := openConfiguredDatabase(context.Background(), path); err == nil {
		t.Fatal("missing SQLite file accepted")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("maintenance initialized SQLite")
	}
}
