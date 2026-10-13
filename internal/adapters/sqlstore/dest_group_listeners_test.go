package sqlstore

import (
	"context"
	"errors"
	"slices"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDestinationGroupListenersProjectOnlyEnabledSniffingWithoutPrivateConfig(t *testing.T) {
	r := newDestDefinitionRepo(t)
	enabled, disabled := true, false
	rows := []nodeRow{{ID: 11, PanelID: 3, InboundID: 11, Enabled: &enabled, Sniffing: "{}", InboundSettings: "private-inbound", StreamSettings: "private-stream"}, {ID: 12, PanelID: 3, InboundID: 12, Enabled: &disabled, Sniffing: "{}"}}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	failed := false
	if err := r.db.Callback().Query().Before("gorm:query").Register("group_listener_projection", func(tx *gorm.DB) {
		if tx.Statement.Table != "nodes" || !slices.Equal(tx.Statement.Selects, []string{"id", "panel_id", "sniffing"}) {
			t.Error("group listener projection loaded private config or unrelated metadata")
		}
		if failed {
			_ = tx.AddError(errors.New("private-listener-driver-detail"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("group_listener_projection") })
	got, err := r.ReadDestinationGroupListeners(t.Context())
	if err != nil || len(got) != 1 || got[0].NodeID != 11 || got[0].PanelID != 3 || got[0].Sniffing != "{}" || len(recorder.queries) != 0 {
		t.Fatal("enabled group listeners lost their narrow, private projection")
	}
	failed = true
	got, err = r.ReadDestinationGroupListeners(t.Context())
	if err != errAuditStorage || got != nil || len(recorder.queries) != 0 {
		t.Fatal("listener read failure returned partial data or private details")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := r.ReadDestinationGroupListeners(ctx); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("listener cancellation lost")
	}
}
