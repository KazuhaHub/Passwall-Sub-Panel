package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestAuditCleanupLoopPurgesLegalConsentOrphans(t *testing.T) {
	store := &connHistoryStore{}
	a := &App{legalConsents: store}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.runAuditCleanupLoop(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, n := store.calls(); n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cutoffs, n := store.calls(); n != 1 || len(cutoffs) != 0 {
		t.Errorf("consent purges=%d retention=%d; want orphan-only cleanup", n, len(cutoffs))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup failed to stop")
	}
}

func TestBuildPrunesLegalConsentOrphans(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()
	a, err := Build(ctx, &config.Config{Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48), ConfigDir: filepath.Join(dir, "config"), DataDir: filepath.Join(dir, "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	if a.legalConsents != a.repos.Legal {
		t.Fatal("Build did not wire the legal consent store into cleanup")
	}
	if err := a.repos.Settings.Save(ctx, ports.UISettings{LegalEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "terms", Locale: "en-US", Content: "test", PublishedBy: 1}); err != nil {
		t.Fatal(err)
	}
	users := make([]*domain.User, 2)
	for i := range users {
		u := &domain.User{UPN: fmt.Sprintf("legal-cleanup-%d@example.test", i), Role: domain.RoleUser, SubToken: fmt.Sprintf("legal-cleanup-sub-%d", i), UUID: fmt.Sprintf("88888888-8888-4888-8888-%012d", i), Enabled: true}
		if err := a.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := a.repos.Legal.Accept(ctx, u.ID, 1); err != nil {
			t.Fatal(err)
		}
		users[i] = u
	}
	if err := a.repos.User.Delete(ctx, users[1].ID); err != nil {
		t.Fatal(err)
	}
	a.pruneLegalConsents(ctx)
	if n, err := a.repos.Legal.PurgeOrphans(ctx); err != nil || n != 0 {
		t.Fatalf("Build cleanup left %d orphans: %v", n, err)
	}
	if s, err := a.repos.Legal.Status(ctx, users[0].ID); err != nil || s.Pending {
		t.Fatalf("live acceptance lost %+v: %v", s, err)
	}
}
