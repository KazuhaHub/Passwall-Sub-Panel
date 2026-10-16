//go:build destination_acceptance && linux

package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

type nativeRetentionSQL struct {
	HourMS          int64 `json:"hour_ms"`
	Rows            int64 `json:"rows"`
	Events          int64 `json:"events"`
	Markers         int64 `json:"markers"`
	ReceivedMS      int64 `json:"received_unix_ms"`
	BudgetRows      int64 `json:"budget_rows"`
	BudgetKeys      int64 `json:"budget_keys"`
	TotalBudgetRows int64 `json:"total_budget_rows"`
}

func nativeRetentionDatabasePath(t *testing.T, a *App) string {
	t.Helper()
	rows, err := a.database.QueryContext(t.Context(), "PRAGMA database_list")
	nativeMust(t, err, "read actual retention database identity")
	defer rows.Close()
	var main string
	for rows.Next() {
		var seq int
		var name, path string
		nativeMust(t, rows.Scan(&seq, &name, &path), "read private retention database filename")
		if name == "main" {
			main = path
		}
	}
	nativeMust(t, rows.Err(), "finish retention database identity")
	root, e1 := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	actual, e2 := filepath.EvalSymlinks(main)
	relative, e3 := filepath.Rel(root, actual)
	if e1 != nil || e2 != nil || e3 != nil || !filepath.IsAbs(actual) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || a.cfg.DBKind() != "sqlite" {
		t.Fatal("retention database is not a persistent owned SQLite file")
	}
	return actual
}

func nativeBuildRetentionFixture(t *testing.T) destinationPolicyFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	info, statErr := os.Stat(root)
	if err != nil || statErr != nil || !filepath.IsAbs(root) || !info.IsDir() || info.Mode().Perm()&0077 != 0 || os.Geteuid() != 0 || os.Getenv("PSP_NATIVE_RETENTION") != "72h" || os.Getenv("PSP_NATIVE_DB_DSN") != "" || os.Getenv("PSP_NATIVE_DB_KIND") != "sqlite" {
		t.Fatal("retention requires explicit isolated root-owned persistent SQLite host")
	}
	dir := filepath.Join(root, "application")
	nativeMust(t, os.Mkdir(dir, 0700), "create new retention application directory")
	cfg, err := nativeDestinationDatabaseConfig("sqlite", config.Config{Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("r", 48), EncryptionKey: strings.Repeat("e", 48), ConfigDir: filepath.Join(dir, "config"), DataDir: filepath.Join(dir, "data")})
	nativeMust(t, err, "configure persistent retention database")
	a, err := buildWithDestinationAuditStore(t.Context(), &cfg, nil)
	nativeMust(t, err, "build actual retention application")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "close original retention application")
		sqlstore.ConfigureSecretKey("")
	})
	nativeVerifyDestinationDatabase(t, a)
	_ = nativeRetentionDatabasePath(t, a)
	return seedDestinationPolicyFixture(t, a)
}

// Reopen the same actual on-disk database and config without reseeding fixtures.
// The transport must already be partitioned before this function is called.
func nativeRetentionRestart(t *testing.T, old *App) *App {
	t.Helper()
	path := nativeRetentionDatabasePath(t, old)
	cfg := *old.cfg
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	nativeMust(t, old.Shutdown(ctx), "actually drain and close original application")
	if old.database.PingContext(ctx) == nil {
		t.Fatal("original retention database pool stayed open after shutdown")
	}
	next, err := buildWithDestinationAuditStore(t.Context(), &cfg, nil)
	nativeMust(t, err, "rebuild application on original persistent database")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		nativeMust(t, next.Shutdown(ctx), "close rebuilt retention application")
	})
	if next == old || nativeRetentionDatabasePath(t, next) != path {
		t.Fatal("retention restart changed the actual database identity")
	}
	return next
}

func nativeRetentionStart(t *testing.T, a *App) {
	t.Helper()
	a.server.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(nativePort(t)))
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "drain restarted retention application")
		select {
		case err := <-done:
			if err != nil && err != http.ErrServerClosed {
				t.Error("restarted retention application failed")
			}
		case <-ctx.Done():
			t.Error("restarted retention application did not stop")
		}
	})
	nativeWait(t, 30*time.Second, "restarted retention production health", func() bool {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + a.server.Addr + "/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	})
}

func nativeRetentionSQLRead(t *testing.T, a *App, agent string, panel int64, request []byte, receivedMS int64) nativeRetentionSQL {
	t.Helper()
	var report protocol.NodeReport
	nativeMust(t, json.Unmarshal(request, &report), "decode original private retention request")
	if report.AgentID != agent || report.Audit == nil || protocol.ValidateAuditObservation(*report.Audit) != nil || len(report.Audit.Hits) != 1 {
		t.Fatal("retention SQL witness needs one actual captured hit")
	}
	audit, hit := report.Audit, report.Audit.Hits[0]
	user, err := hit.Subject.RowID()
	nativeMust(t, err, "resolve actual retention subject")
	var result nativeRetentionSQL
	result.HourMS = audit.Hour
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COUNT(*),COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=? AND user_id=? AND hour_ms=? AND source=? AND action=? AND dest=? AND port=?", panel, user, audit.Hour, hit.RuleID, hit.Action, hit.Dest, hit.Port).Scan(&result.Rows, &result.Events), "read exact original hit ledger")
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM dest_audit_batches WHERE agent_id=? AND batch_id=? AND kind=? AND hour_ms=?", agent, audit.BatchID, audit.Kind, audit.Hour).Scan(&result.Markers), "read original durable marker")
	if result.Markers == 1 {
		var received time.Time
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT received_at FROM dest_audit_batches WHERE agent_id=? AND batch_id=?", agent, audit.BatchID).Scan(&received), "read actual original receipt time")
		result.ReceivedMS = received.UnixMilli()
	}
	if receivedMS == 0 {
		receivedMS = result.ReceivedMS
	}
	if receivedMS <= 0 {
		t.Fatal("retention budget lacks original receipt anchor")
	}
	hour := time.UnixMilli(receivedMS).UTC().Truncate(time.Hour).UnixMilli()
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COUNT(*),COALESCE(SUM(rows_reserved),0) FROM dest_audit_ingest_budget WHERE agent_id=? AND kind=? AND received_hour_ms=?", agent, audit.Kind, hour).Scan(&result.BudgetKeys, &result.BudgetRows), "read persistent original-hour lane budget")
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(rows_reserved),0) FROM dest_audit_ingest_budget WHERE agent_id=? AND kind=?", agent, audit.Kind).Scan(&result.TotalBudgetRows), "read all-hour quota consumption")
	return result
}
