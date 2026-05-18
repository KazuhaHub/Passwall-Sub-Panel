package mysql

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
	if totalPlain > 0 && len(dbSecretKey) == 0 {
		log.Warn("secrets audit summary: encryption key not configured — sensitive credentials stored in plaintext",
			"plaintext_rows_total", totalPlain,
			"action", "set PSP_SECRET_KEY_MATERIAL (≥32 random bytes) and re-save SAML / OIDC / SMTP / 3X-UI settings")
	}
}
