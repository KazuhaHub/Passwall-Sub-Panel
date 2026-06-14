package sqlstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func newCertReposTest(t *testing.T) (ports.Repos, *gorm.DB, context.Context) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return NewRepos(db), db, context.Background()
}

func TestDNSCredentialRoundTripAndEncryptedAtRest(t *testing.T) {
	ConfigureSecretKey("test-db-secret")
	t.Cleanup(func() { ConfigureSecretKey("") })
	repos, db, ctx := newCertReposTest(t)

	cred := &domain.DNSCredential{
		Name:        "cf-main",
		Provider:    "cloudflare",
		Credentials: map[string]string{"CF_DNS_API_TOKEN": "secret-tok"},
	}
	if err := repos.DNSCredential.Create(ctx, cred); err != nil {
		t.Fatalf("create: %v", err)
	}
	if cred.ID == 0 {
		t.Fatal("id not backfilled")
	}

	// Encrypted at rest: the raw column must be enc-prefixed and must NOT
	// contain the plaintext token.
	var row dnsCredentialRow
	if err := db.First(&row, cred.ID).Error; err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if !strings.HasPrefix(row.Credentials, secretPrefix) || strings.Contains(row.Credentials, "secret-tok") {
		t.Fatalf("credentials not encrypted at rest: %q", row.Credentials)
	}

	got, err := repos.DNSCredential.GetByID(ctx, cred.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Provider != "cloudflare" || got.Credentials["CF_DNS_API_TOKEN"] != "secret-tok" {
		t.Fatalf("decrypted credential = %#v", got)
	}
	list, err := repos.DNSCredential.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %#v, err %v", list, err)
	}
}

func TestACMEAccountCRUDRoundTrip(t *testing.T) {
	repos, _, ctx := newCertReposTest(t)

	// Create an EAB account; AccountKey + EABHMACKey are encrypted at rest.
	acc := &domain.ACMEAccount{
		Name: "ZeroSSL", Email: "a@b.c", Directory: "https://acme/dir",
		EABKeyID: "kid", EABHMACKey: "hmac", KeyType: "RSA2048",
	}
	if err := repos.ACMEAccount.Create(ctx, acc); err != nil {
		t.Fatalf("create: %v", err)
	}
	if acc.ID == 0 {
		t.Fatal("create must set the id")
	}

	got, err := repos.ACMEAccount.GetByID(ctx, acc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "ZeroSSL" || got.EABKeyID != "kid" || got.EABHMACKey != "hmac" || got.KeyType != "RSA2048" {
		t.Fatalf("account round-trip = %#v", got)
	}

	// Registration write-back, then config Update must NOT clobber it.
	if err := repos.ACMEAccount.UpdateRegistration(ctx, acc.ID, "PEMKEY", `{"uri":"x"}`); err != nil {
		t.Fatalf("update registration: %v", err)
	}
	acc.Name = "ZeroSSL renamed"
	if err := repos.ACMEAccount.Update(ctx, acc); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = repos.ACMEAccount.GetByID(ctx, acc.ID)
	if got.Name != "ZeroSSL renamed" || got.AccountKey != "PEMKEY" || got.Registration != `{"uri":"x"}` {
		t.Fatalf("config update must preserve the lazy machine fields: %#v", got)
	}

	// ClearRegistration drops the lazy fields.
	if err := repos.ACMEAccount.ClearRegistration(ctx, acc.ID); err != nil {
		t.Fatalf("clear registration: %v", err)
	}
	got, _ = repos.ACMEAccount.GetByID(ctx, acc.ID)
	if got.AccountKey != "" || got.Registration != "" {
		t.Fatalf("clear must drop account key + registration: %#v", got)
	}

	// List + Delete.
	list, err := repos.ACMEAccount.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d (%v)", len(list), err)
	}
	if err := repos.ACMEAccount.Delete(ctx, acc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repos.ACMEAccount.GetByID(ctx, acc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted account must be ErrNotFound, got %v", err)
	}
}

func TestCertificateCreateGetListByStatus(t *testing.T) {
	repos, _, ctx := newCertReposTest(t)

	c := &domain.TLSCertificate{Name: "wild", Domains: []string{"example.com", "*.example.com"}, Status: domain.CertStatusPending, AutoRenew: true}
	if err := repos.Certificate.Create(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repos.Certificate.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Domains) != 2 || got.Domains[1] != "*.example.com" || !got.AutoRenew {
		t.Fatalf("cert round-trip = %#v", got)
	}

	c2 := &domain.TLSCertificate{Name: "two", Domains: []string{"two.com"}, Status: domain.CertStatusActive}
	if err := repos.Certificate.Create(ctx, c2); err != nil {
		t.Fatalf("create2: %v", err)
	}
	active, err := repos.Certificate.ListByStatus(ctx, domain.CertStatusActive)
	if err != nil {
		t.Fatalf("listByStatus: %v", err)
	}
	if len(active) != 1 || active[0].Name != "two" {
		t.Fatalf("active = %#v", active)
	}
}

// UpdateIssued writes only the issuance-owned columns. A renewal worker holding
// a stale copy must not roll back admin-owned columns (name / auto_renew /
// domains) that were edited after the worker loaded its snapshot. Mirrors the
// nodes.UpdateHealth / xui_panels.UpdateVersion column-scoping invariant.
func TestCertificateUpdateIssuedIsColumnScoped(t *testing.T) {
	repos, _, ctx := newCertReposTest(t)

	c := &domain.TLSCertificate{Name: "wild", Domains: []string{"example.com"}, Status: domain.CertStatusPending, AutoRenew: true}
	if err := repos.Certificate.Create(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Concurrent admin edit AFTER the worker loaded its copy.
	edit, _ := repos.Certificate.GetByID(ctx, c.ID)
	edit.Name = "renamed"
	edit.AutoRenew = false
	if err := repos.Certificate.Update(ctx, edit); err != nil {
		t.Fatalf("admin update: %v", err)
	}
	// Worker writes the issuance result from its STALE copy (old name, autoRenew).
	na := time.Unix(2000000000, 0)
	issued := &domain.TLSCertificate{
		ID: c.ID, Name: "wild", AutoRenew: true, // stale admin columns — must be ignored
		CertPEM: "CERT", KeyPEM: "KEY", Status: domain.CertStatusActive, Fingerprint: "fp", NotAfter: &na,
	}
	if err := repos.Certificate.UpdateIssued(ctx, issued); err != nil {
		t.Fatalf("updateIssued: %v", err)
	}

	final, _ := repos.Certificate.GetByID(ctx, c.ID)
	if final.Status != domain.CertStatusActive || final.CertPEM != "CERT" || final.Fingerprint != "fp" || final.NotAfter == nil {
		t.Fatalf("issuance columns not applied: %#v", final)
	}
	if final.Name != "renamed" || final.AutoRenew != false {
		t.Fatalf("UpdateIssued clobbered admin columns: name=%q autoRenew=%v", final.Name, final.AutoRenew)
	}
}

func TestCertificateKeyEncryptedAtRest(t *testing.T) {
	ConfigureSecretKey("test-db-secret")
	t.Cleanup(func() { ConfigureSecretKey("") })
	repos, db, ctx := newCertReposTest(t)

	c := &domain.TLSCertificate{Name: "x", Domains: []string{"x.com"}, CertPEM: "-----BEGIN CERT-----", KeyPEM: "-----BEGIN KEY-----", Status: domain.CertStatusActive}
	if err := repos.Certificate.Create(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	var row tlsCertificateRow
	if err := db.First(&row, c.ID).Error; err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if !strings.HasPrefix(row.KeyPEM, secretPrefix) || strings.Contains(row.KeyPEM, "BEGIN KEY") {
		t.Fatalf("key not encrypted at rest: %q", row.KeyPEM)
	}
	got, _ := repos.Certificate.GetByID(ctx, c.ID)
	if got.KeyPEM != "-----BEGIN KEY-----" {
		t.Fatalf("key not decrypted on read: %q", got.KeyPEM)
	}
}
