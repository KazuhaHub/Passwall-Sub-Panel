package sqlstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestPanelSaveCannotOverwriteAuditCollect(t *testing.T) {
	r := newPanelRepo(t)
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "collect", URL: "psp://agt_collect"}
	if err := r.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Model(&xuiPanelRow{}).Where("id = ?", p.ID).Updates(map[string]any{"audit_collect": "off", "audit_collect_revision": int64(42)}).Error; err != nil {
		t.Fatal(err)
	}
	// This stale display/credential edit predates the separate collection write.
	p.Remark = "updated display"
	if err := r.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	var row struct {
		AuditCollect         string
		AuditCollectRevision int64
		Remark               string
	}
	if err := r.db.Table("xui_panels").Where("id = ?", p.ID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.AuditCollect != "off" || row.AuditCollectRevision != 42 || row.Remark != p.Remark {
		t.Fatalf("ordinary Save overwrote collection control: %+v", row)
	}
}

type auditMetadataWriter interface {
	UpdateNativeMetadata(context.Context, int64, *string, *string, *domain.PanelUpdateChannel, *domain.AuditCollect) error
}

func TestNativeAuditCollectRevisionChangesOnlyWithTheMode(t *testing.T) {
	r := newPanelRepo(t)
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "revision", URL: "psp://agt_revision"}
	if err := r.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if p.AuditCollect != domain.AuditCollectHits || p.AuditCollectRevision != 1 {
		t.Fatalf("create did not initialize collection: %+v", p)
	}
	w, ok := any(r).(auditMetadataWriter)
	if !ok {
		t.Fatal("native metadata writer has no collection control")
	}
	off := domain.AuditCollectOff
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() { errs <- w.UpdateNativeMetadata(t.Context(), p.ID, nil, nil, nil, &off) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.GetByID(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuditCollect != off || got.AuditCollectRevision != 2 {
		t.Fatalf("concurrent equal writes changed revision more than once: %+v", got)
	}
	hits := domain.AuditCollectHits
	if err := w.UpdateNativeMetadata(t.Context(), p.ID, nil, nil, nil, &hits); err != nil {
		t.Fatal(err)
	}
	got, err = r.GetByID(t.Context(), p.ID)
	if err != nil || got.AuditCollectRevision != 3 {
		t.Fatalf("re-enable must invalidate old batches: %+v, %v", got, err)
	}
	invalid := domain.AuditCollect("invalid")
	if err := w.UpdateNativeMetadata(t.Context(), p.ID, nil, nil, nil, &invalid); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid mode accepted: %v", err)
	}
	upstream := &domain.Panel{Kind: domain.PanelKind3XUI, Name: "upstream", URL: "https://panel.example"}
	if err := r.Save(t.Context(), upstream); err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateNativeMetadata(t.Context(), upstream.ID, nil, nil, nil, &off); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("upstream collection write accepted: %v", err)
	}
}
