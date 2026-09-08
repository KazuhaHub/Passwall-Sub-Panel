package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// created_at is immutable, and a full-row Save writes every column — including
// the ones the caller never filled in. Where an admin edit builds its domain
// object from the request DTO rather than loading the stored row, that
// combination silently replaces the creation timestamp with Go's zero time.
//
// Two repositories already guard against it (userRepo omits the poll-owned
// columns, xuiPanelRepo omits CreatedAt) so the hazard was known; separatorRepo
// and dnsCredentialRepo simply had not been given the same treatment. Nothing
// failed and nothing warned in either case — the value was just gone.
//
// This is one table so a repository added later is cheap to include, and so the
// rule is stated once rather than rediscovered per repo.
func TestUpdatePreservesCreatedAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		// create stores a row and returns its id; update edits it the way the
		// admin handler does — building a fresh object from request fields,
		// with no created_at; read returns the stored created_at.
		run func(t *testing.T, r reposUnderTest)
	}{
		{"separator", func(t *testing.T, r reposUnderTest) {
			ctx := context.Background()
			e := &domain.SeparatorEntry{DisplayName: "---- TW ----", Enabled: true, Mode: domain.SeparatorModeGlobal}
			if err := r.sep.Create(ctx, e); err != nil {
				t.Fatalf("create: %v", err)
			}
			before, err := r.sep.GetByID(ctx, e.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if before.CreatedAt.IsZero() {
				t.Fatal("create did not stamp created_at; this test would prove nothing")
			}
			// Exactly what admin_node.go builds: no CreatedAt field exists on
			// the request.
			if err := r.sep.Update(ctx, &domain.SeparatorEntry{
				ID: e.ID, DisplayName: "---- TW ----", Enabled: false, Mode: domain.SeparatorModeGlobal,
			}); err != nil {
				t.Fatalf("update: %v", err)
			}
			after, err := r.sep.GetByID(ctx, e.ID)
			if err != nil {
				t.Fatalf("get after: %v", err)
			}
			if !after.CreatedAt.Equal(before.CreatedAt) {
				t.Fatalf("created_at = %v, want %v", after.CreatedAt, before.CreatedAt)
			}
			if after.Enabled {
				t.Fatal("the edit itself did not land")
			}
		}},
		{"dns_credential", func(t *testing.T, r reposUnderTest) {
			ctx := context.Background()
			c := &domain.DNSCredential{
				Name: "cf", Provider: "cloudflare",
				Credentials: map[string]string{"CF_DNS_API_TOKEN": "tok"},
			}
			if err := r.dns.Create(ctx, c); err != nil {
				t.Fatalf("create: %v", err)
			}
			before, err := r.dns.GetByID(ctx, c.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if before.CreatedAt.IsZero() {
				t.Fatal("create did not stamp created_at; this test would prove nothing")
			}
			// Exactly what admin_cert.go builds for a rename.
			if err := r.dns.Update(ctx, &domain.DNSCredential{
				ID: c.ID, Name: "cf-prod", Provider: "cloudflare",
				Credentials: map[string]string{"CF_DNS_API_TOKEN": "tok"},
			}); err != nil {
				t.Fatalf("update: %v", err)
			}
			after, err := r.dns.GetByID(ctx, c.ID)
			if err != nil {
				t.Fatalf("get after: %v", err)
			}
			if !after.CreatedAt.Equal(before.CreatedAt) {
				t.Fatalf("created_at = %v, want %v", after.CreatedAt, before.CreatedAt)
			}
			if after.Name != "cf-prod" {
				t.Fatalf("the edit itself did not land: name = %q", after.Name)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := openTestDB(t)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			sqlDB, _ := db.DB()
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := EnsureSchema(db); err != nil {
				t.Fatalf("schema: %v", err)
			}
			tc.run(t, reposUnderTest{
				sep: &separatorRepo{db: db},
				dns: &dnsCredentialRepo{db: db},
			})
		})
	}
}

type reposUnderTest struct {
	sep *separatorRepo
	dns *dnsCredentialRepo
}
