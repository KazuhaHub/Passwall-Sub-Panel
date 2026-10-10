package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestShutdownClosesDatabaseOnlyAfterBackgroundReadersExit(t *testing.T) {
	directory := t.TempDir()
	a, err := Build(t.Context(), &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(ctx)
		sqlstore.ConfigureSecretKey("")
	})
	readResult := make(chan error, 1)
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		<-a.bgRootCtx.Done()
		_, err := a.repos.User.GetByID(context.Background(), 999999)
		readResult <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-readResult; !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("background reader lost the database before draining: %v", err)
	}
	if _, err := a.repos.User.GetByID(context.Background(), 999999); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Shutdown left the database connection open: %v", err)
	}
}

func TestShutdownKeepsDatabaseOpenUntilAdmittedOperationsDrain(t *testing.T) {
	a := buildDestinationListsFixture(t)
	admitted, release, err := a.operationGate.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(release) }
	defer finish()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done <- a.Shutdown(ctx)
	}()
	select {
	case <-a.bgRootCtx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not cancel background work")
	}
	returned := false
	select {
	case err := <-done:
		returned = true
		t.Errorf("Shutdown returned before an admitted operation drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := a.repos.User.GetByID(admitted, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("admitted operation lost the database during shutdown: %v", err)
	}
	finish()
	if !returned {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Shutdown did not finish after admission drained")
		}
	}
	if _, err := a.repos.User.GetByID(context.Background(), 999999); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("database remained open after all operations drained: %v", err)
	}
}

func TestShutdownDeadlineRetainsDatabaseUntilAdmittedWorkEnds(t *testing.T) {
	a := buildDestinationListsFixture(t)
	admitted, release, err := a.operationGate.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(release) }
	defer finish()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = a.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("shutdown deadline reported success: %v", err)
	}
	if _, err := a.repos.User.GetByID(admitted, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deadline closed database beneath admitted work: %v", err)
	}
	finish()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := a.database.PingContext(t.Context()); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("database did not close after timed-out shutdown's admitted work ended")
}
