package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// Run the real entrypoint in a child process: main uses os.Exit for commands,
// and testing only a message helper would not catch a guard accidentally moved
// below LoadOrGenerate, seed.Ensure or app.Build.
func TestPanelCLIHelperProcess(t *testing.T) {
	if os.Getenv("PSP_TEST_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"psp"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("helper invocation has no argument separator")
}

func runPanelCLI(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	argv := append([]string{"-test.run=^TestPanelCLIHelperProcess$", "--"}, args...)
	cmd := exec.CommandContext(ctx, executable, argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PSP_TEST_CLI_HELPER=1", "PSP_CONFIG="+filepath.Join(dir, "config.yaml"), "PSP_MYSQL_DSN=", "PSP_POSTGRES_DSN=")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("CLI did not exit before panel boot: %v\n%s", ctx.Err(), out)
	}
	if err == nil {
		return string(out), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run CLI: %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode()
}

func TestRetiredMigrateDoesNotInitializePanel(t *testing.T) {
	for _, args := range [][]string{
		{"migrate"},
		{"migrate", "--help"},
		{"migrate", "--driver=sqlite", "--src=old.db", "--dst=new.db"},
		{"migrate", "--config=custom.yaml"},
		{"--config=custom.yaml", "migrate"},
		{"--config", "custom.yaml", "migrate", "--driver=sqlite", "--src=old.db", "--dst=new.db"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			out, code := runPanelCLI(t, dir, args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2\n%s", code, out)
			}
			for _, text := range []string{"retired in V4", "frozen PSP v3.9.2", "automatically on normal startup"} {
				if !strings.Contains(out, text) {
					t.Errorf("missing guidance %q in %s", text, out)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("retired command initialized config/data or migration files: %v", entries)
			}
		})
	}
}

func TestUnknownPositionalArgsDoNotInitializePanel(t *testing.T) {
	for _, args := range [][]string{
		{"unknown-command"},
		{"--config", "custom.yaml", "unknown-command"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			out, code := runPanelCLI(t, dir, args...)
			if code != 2 || !strings.Contains(out, "unexpected positional arguments") {
				t.Fatalf("unknown arguments entered boot: exit=%d\n%s", code, out)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unknown arguments initialized panel files: %v, %v", entries, err)
			}
		})
	}
}

func TestServerMigrationDispatchNeverInitializesPanel(t *testing.T) {
	for _, args := range [][]string{{"migrate-server", "--help"}, {"migrate-server"}, {"migrate-server", "--server-id", "12", "--apply"}, {"--config", "x.yaml", "migrate-server"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			out, code := runPanelCLI(t, dir, args...)
			want := 2
			if len(args) == 2 && args[1] == "--help" {
				want = 0
			}
			if code != want {
				t.Fatalf("code=%d want=%d %s", code, want, out)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("maintenance started/configured panel: %v err=%v", entries, err)
			}
		})
	}
}

func TestRetiredMigrateDoesNotLoadExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	invalidConfig := []byte("listen: [intentionally invalid YAML\n")
	if err := os.WriteFile(path, invalidConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runPanelCLI(t, dir, "migrate", "--dry-run")
	if code != 2 || strings.Contains(out, "load config") {
		t.Fatalf("retired command attempted normal config loading: exit=%d\n%s", code, out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, invalidConfig) {
		t.Fatal("retired command modified existing config")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("retired command initialized panel files: %v, %v", entries, err)
	}
}

func TestRemainingReadOnlyCLIDispatchDoesNotBootPanel(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		text string
	}{
		{[]string{"version"}, 0, version.String()},
		{[]string{"--version"}, 0, version.String()},
		{[]string{"-v"}, 0, version.String()},
		{[]string{"normalize-upn", "--help"}, 2, "DRY RUN BY DEFAULT"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			dir := t.TempDir()
			out, code := runPanelCLI(t, dir, tc.args...)
			if code != tc.code || !strings.Contains(out, tc.text) {
				t.Fatalf("unexpected command dispatch: exit=%d\n%s", code, out)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("read-only command initialized panel files: %v, %v", entries, err)
			}
		})
	}
}
