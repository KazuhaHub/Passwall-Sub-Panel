package sqlstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func TestPanelAuditSettingsReadNarrowCurrentControl(t *testing.T) {
	r := newPanelRepo(t)
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "narrow-settings", URL: "psp://agt_settings"}
	if err := r.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	queries := 0
	r.db.Callback().Query().After("gorm:query").Register("audit-settings-narrow", func(tx *gorm.DB) {
		if tx.Statement.Table != "xui_panels" {
			return
		}
		queries++
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "SELECT *") || strings.Contains(sql, "username") || strings.Contains(sql, "password") || strings.Contains(sql, "token") || strings.Contains(sql, "url") {
			tx.AddError(errors.New("collection lookup materialized panel credentials"))
		}
	})
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("audit-settings-narrow") })
	settings, err := r.GetAuditSettings(t.Context(), p.ID)
	if err != nil || settings.Collect != domain.AuditCollectHits || settings.Revision != 1 || queries != 1 {
		t.Fatalf("initial narrow settings: %+v, queries=%d / %v", settings, queries, err)
	}
	off := domain.AuditCollectOff
	if err := r.UpdateNativeMetadata(t.Context(), p.ID, nil, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	settings, err = r.GetAuditSettings(t.Context(), p.ID)
	if err != nil || settings.Collect != off || settings.Revision != 2 {
		t.Fatalf("stale collection input: %+v / %v", settings, err)
	}
	if _, err := r.GetAuditSettings(t.Context(), p.ID+100); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing owner: %v", err)
	}
	if _, err := r.GetAuditSettings(t.Context(), 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid owner: %v", err)
	}
	for _, update := range []map[string]any{{"audit_collect_revision": int64(0)}, {"audit_collect_revision": int64(2), "audit_collect": "invalid"}, {"audit_collect": "off", "kind": string(domain.PanelKind3XUI)}} {
		if err := r.db.Model(&xuiPanelRow{}).Where("id = ?", p.ID).Updates(update).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := r.GetAuditSettings(t.Context(), p.ID); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("invalid native control accepted: %v", err)
		}
	}
}
