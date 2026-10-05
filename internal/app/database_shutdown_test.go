package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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
