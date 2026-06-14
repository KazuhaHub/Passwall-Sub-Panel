package mysql

import (
	"context"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestXUIPanelSecretsEncryptedAtRest(t *testing.T) {
	ConfigureSecretKey("test-db-secret")
	t.Cleanup(func() { ConfigureSecretKey("") })

	db, err := openTestDB(t)
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
	// The boot path (SAML/OIDC/SMTP Load) aborts on this error, so it must
	// guide recovery — the classic cause is rotating jwt_secret on a legacy
	// config where it doubles as the at-rest key. Assert the hint is present
	// rather than a cryptic GCM failure.
	if !strings.Contains(err.Error(), "jwt_secret") {
		t.Fatalf("decrypt-with-wrong-key error must guide recovery (mention jwt_secret), got: %v", err)
	}
}

// The at-rest audit must also cover encrypted-at-rest settings in the KV
// `settings` table (captcha_secret_key, geo_ip_update_token, ...), not just the
// single-row credential tables. A row flagged Encrypted but stored without the
// enc:v1: prefix is plaintext-at-rest and must be counted; a properly encrypted
// row and a non-encrypted setting must not.
func TestCountPlaintextEncryptedSettings(t *testing.T) {
	ConfigureSecretKey("test-db-secret")
	t.Cleanup(func() { ConfigureSecretKey("") })
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	rows := []settingRow{
		{Type: "security", Name: "captcha_secret_key", Value: "PLAINTEXTSECRET", Encrypted: true},     // leak — must count
		{Type: "geo", Name: "geo_ip_update_token", Value: secretPrefix + "deadbeef", Encrypted: true}, // ok
		{Type: "ui", Name: "brand_name", Value: "Acme Corp", Encrypted: false},                        // not a secret
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	n, err := countPlaintextEncryptedSettings(db)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("plaintext encrypted-setting count = %d, want 1 (only the unencrypted captcha_secret_key)", n)
	}
}
