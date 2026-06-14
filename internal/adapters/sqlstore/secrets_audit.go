package sqlstore

import (
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// AuditSecretsAtRest scans every column that holds an encrypted secret
// and counts rows where the value is non-empty but lacks the enc:v1:
// prefix. These rows are stored in plaintext — either because the panel
// ran without PSP_SECRET_KEY_MATERIAL configured (encryptSecret silently
// returns plaintext in that mode) or because a row predates the
// encryption rollout.
//
// Run at boot AFTER ConfigureSecretKey. The function only logs; it
// never modifies rows because re-encrypting silently could mask a real
// configuration error (admin pointed at a fresh DB by accident, etc.).
// The admin sees a single WARN per affected column with a row count and
// a hint to either set the secret material or re-save the affected
// admin UI page.
func AuditSecretsAtRest(db *gorm.DB) {
	if db == nil {
		return
	}
	// (table, column, label) tuples. Label appears in the log for the
	// admin to know what to re-save in the UI.
	checks := []struct {
		table  string
		column string
		label  string
	}{
		{"xui_panels", "api_token", "3X-UI panel API token"},
		{"xui_panels", "password", "3X-UI panel password"},
		{"saml_settings", "sp_private_key", "SAML SP private key"},
		{"oidc_settings", "client_secret", "OIDC client secret"},
		{"mail_settings", "smtp_password", "SMTP password"},
		// v3.5 inbound config snapshot — SS-2022 server PSK lives in settings
		// (top-level `password`), Reality privateKey + inline TLS keys live
		// in stream_settings. Pre-v3.5 rows are plaintext until next re-save.
		{"nodes", "inbound_settings", "inbound settings (SS-2022 server PSK)"},
		{"nodes", "stream_settings", "inbound stream settings (Reality / TLS keys)"},
	}
	totalPlain := 0
	for _, c := range checks {
		if !db.Migrator().HasTable(c.table) {
			continue
		}
		if !db.Migrator().HasColumn(c.table, c.column) {
			continue
		}
		var count int64
		q := db.Table(c.table).
			Where(c.column+" <> ''").
			Where(c.column+" NOT LIKE ?", secretPrefix+"%")
		if err := q.Count(&count).Error; err != nil {
			log.Warn("secrets audit query failed", "table", c.table, "column", c.column, "err", err)
			continue
		}
		if count == 0 {
			continue
		}
		totalPlain += int(count)
		hint := "set PSP_SECRET_KEY_MATERIAL and re-save the affected admin UI page"
		if len(dbSecretKey) == 0 {
			hint = "PSP_SECRET_KEY_MATERIAL is not configured — set it before deploying to production"
		}
		log.Warn("secrets-at-rest audit: plaintext rows detected",
			"label", c.label,
			"table", c.table,
			"column", c.column,
			"rows", count,
			"hint", hint)
	}
	// KV `settings` table: rows flagged encrypted-at-rest (captcha_secret_key,
	// geo_ip_update_token, …) but stored without the enc:v1: prefix — the same
	// plaintext-at-rest condition as the columns above. The table is a generic
	// (type,name,value,encrypted) KV, so it's matched by the Encrypted flag
	// rather than a fixed column.
	if n, err := countPlaintextEncryptedSettings(db); err != nil {
		log.Warn("secrets audit query failed", "table", "settings", "column", "value", "err", err)
	} else if n > 0 {
		totalPlain += int(n)
		hint := "set PSP_SECRET_KEY_MATERIAL and re-save the affected admin UI page"
		if len(dbSecretKey) == 0 {
			hint = "PSP_SECRET_KEY_MATERIAL is not configured — set it before deploying to production"
		}
		log.Warn("secrets-at-rest audit: plaintext rows detected",
			"label", "encrypted KV settings (e.g. captcha secret / geo update token)",
			"table", "settings",
			"column", "value",
			"rows", n,
			"hint", hint)
	}

	if totalPlain > 0 && len(dbSecretKey) == 0 {
		log.Warn("secrets audit summary: encryption key not configured — sensitive credentials stored in plaintext",
			"plaintext_rows_total", totalPlain,
			"action", "set PSP_SECRET_KEY_MATERIAL (≥32 random bytes) and re-save SAML / OIDC / SMTP / 3X-UI settings")
	}
}

// countPlaintextEncryptedSettings counts rows in the KV `settings` table that
// are flagged encrypted-at-rest but whose value lacks the enc:v1: prefix — i.e.
// stored plaintext (no PSP_SECRET_KEY_MATERIAL, or pre-encryption rows). The
// Encrypted column is written from the setting descriptor regardless of whether
// the value actually got encrypted, so this flag reliably identifies rows that
// SHOULD be ciphertext.
func countPlaintextEncryptedSettings(db *gorm.DB) (int64, error) {
	if db == nil || !db.Migrator().HasTable("settings") {
		return 0, nil
	}
	var count int64
	err := db.Table("settings").
		Where("encrypted = ?", true).
		Where("value <> ''").
		Where("value NOT LIKE ?", secretPrefix+"%").
		Count(&count).Error
	return count, err
}
