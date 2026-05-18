package mysql

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestXUIPanelSecretsEncryptedAtRest(t *testing.T) {
	ConfigureSecretKey("test-db-secret")
	t.Cleanup(func() { ConfigureSecretKey("") })

	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap db: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	repo := NewRepos(db).XUIPanel
	ctx := context.Background()
	panel := &domain.XUIPanel{
		Name:     "main",
		URL:      "https://xui.example.test",
		APIToken: "api-token",
		Username: "admin",
		Password: "panel-password",
	}
	if err := repo.Save(ctx, panel); err != nil {
		t.Fatalf("save panel: %v", err)
	}

	var row xuiPanelRow
	if err := db.First(&row, panel.ID).Error; err != nil {
		t.Fatalf("read raw row: %v", err)
	}
	if !strings.HasPrefix(row.APIToken, secretPrefix) || row.APIToken == panel.APIToken {
		t.Fatalf("api token not encrypted at rest: %q", row.APIToken)
	}
	if !strings.HasPrefix(row.Password, secretPrefix) || row.Password == panel.Password {
		t.Fatalf("password not encrypted at rest: %q", row.Password)
	}

	got, err := repo.GetByID(ctx, panel.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.APIToken != panel.APIToken || got.Password != panel.Password {
		t.Fatalf("decrypted panel = %#v, want api/password plaintext", got)
	}
}

// TestDecryptSecretRequiresKey: dropping the key after writing
// encrypted rows must make subsequent reads fail explicitly, not
// silently return ciphertext-as-plaintext. Catches the regression
// where a misconfigured deployment loses PSP_SECRET_KEY_MATERIAL
// and starts vending the literal `enc:v1:...` string to handlers.
func TestDecryptSecretRequiresKey(t *testing.T) {
	ConfigureSecretKey("strong-test-secret-AAA")
	ciphertext, err := encryptSecret("password-value")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(ciphertext, secretPrefix) {
		t.Fatalf("encrypt produced unprefixed output: %q", ciphertext)
	}
	// Now wipe the key — simulates a panel restart with the env var
	// stripped from the systemd unit.
	ConfigureSecretKey("")
	t.Cleanup(func() { ConfigureSecretKey("") })
	got, err := decryptSecret(ciphertext)
	if err == nil {
		t.Fatalf("decryptSecret without key should error, got %q", got)
	}
	if got != "" {
		t.Fatalf("error path leaked plaintext: %q", got)
	}
}

// TestEncryptSecretPlaintextWithoutKey: documents the deliberate
// "no key configured ⇒ store plaintext" behaviour. The point of the
// test is to lock that behaviour: a future change that flips it to
// "error out without a key" would break first-launch flows that
// haven't set the secret yet, so this is a guard-rail, not a defect.
func TestEncryptSecretPlaintextWithoutKey(t *testing.T) {
	ConfigureSecretKey("")
	t.Cleanup(func() { ConfigureSecretKey("") })
	got, err := encryptSecret("plain")
	if err != nil {
		t.Fatalf("encrypt without key: %v", err)
	}
	if got != "plain" {
		t.Fatalf("expected plaintext passthrough without key, got %q", got)
	}
	// And decrypting the same value (without the prefix) is a no-op.
	out, err := decryptSecret("plain")
	if err != nil {
		t.Fatalf("decrypt plaintext: %v", err)
	}
	if out != "plain" {
		t.Fatalf("decrypt round-trip = %q, want plain", out)
	}
}

// TestEncryptSecretRoundTripDifferentKey: encrypting with one key
// then trying to decrypt with another must fail closed. Catches a
// silent-decrypt regression where a key rotation would otherwise
// surface as "passwords look weird in admin UI" instead of an
// auditable error.
func TestEncryptSecretRoundTripDifferentKey(t *testing.T) {
	ConfigureSecretKey("key-aaa-aaa-aaa-aaa")
	ciphertext, err := encryptSecret("payload")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ConfigureSecretKey("key-bbb-bbb-bbb-bbb")
	t.Cleanup(func() { ConfigureSecretKey("") })
	got, err := decryptSecret(ciphertext)
	if err == nil {
		t.Fatalf("decrypt with wrong key should error, got %q", got)
	}
}
