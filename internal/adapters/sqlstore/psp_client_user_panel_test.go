package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestPSPClientPanelReadKeepsFullCurrentRowsAndFailureSemantics(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	var rows []pspClientRow
	for panel := int64(65); panel > 0; panel-- {
		for partition := int64(3); partition > 0; partition-- {
			id := panel*10 + partition
			user := int64(7)
			if partition == 3 {
				user = 99
			}
			rows = append(rows, pspClientRow{ID: id, UserID: user, PanelID: panel,
				Email: fmt.Sprintf("panel-scope-%d@example.test", id), CredClass: int(partition), UUID: fmt.Sprintf("uuid-%d", id), Password: fmt.Sprintf("secret-%d", id),
				DesiredEnable: id%2 == 0, DesiredExpiryTime: 1893456000000 + id, PanelQuotaHeadroom: 1<<60 + id,
				PanelIPLimit: int(partition), PanelDeviceLimit: int(panel), DesiredMinted: id%3 == 0, CreatedAt: time.Unix(1700000000+id, 0).UTC(),
				LifetimeUpBytes: 1<<60 + id, LifetimeDownBytes: 1<<59 + id, LifetimeTotalBytes: 1<<61 + id,
				LastRawUpBytes: id * 3, LastRawDownBytes: id * 5, LastRawTotalBytes: id * 8, LastCounterEpoch: uint64(id + 100),
				PeriodBaselineUpBytes: id * 2, PeriodBaselineDownBytes: id * 4, PeriodBaselineTotalBytes: id * 6})
		}
	}
	if err := r.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	var readRows []int64
	const observer = "user-panel-read-cardinality"
	if err := r.db.Callback().Query().After("gorm:query").Register(observer, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_clients" {
			readRows = append(readRows, tx.RowsAffected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove(observer) })
	assertRead := func(user, panel int64) []*domain.PSPClient {
		t.Helper()
		all, err := r.ListByUser(t.Context(), user)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]*domain.PSPClient, 0)
		for _, c := range all {
			if c.PanelID == panel {
				want = append(want, c)
			}
		}
		before := len(readRows)
		got, err := r.ListByUserAndPanel(t.Context(), user, panel)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("panel read changed complete fields, tenant isolation or stable ordering")
		}
		if len(readRows) != before+1 || readRows[before] != int64(len(want)) {
			t.Fatal("panel read decoded unrelated rows or issued additional queries")
		}
		return got
	}
	for _, user := range []int64{7, 99, 999} {
		for _, panel := range []int64{1, 17, 65, 999} {
			assertRead(user, panel)
		}
	}
	before := assertRead(7, 17)
	if len(before) != 2 {
		t.Fatal("missing independent credential partitions")
	}
	id := before[0].ID
	before[0].Password = "mutated caller copy"
	if err := r.db.Model(&pspClientRow{}).Where("id = ?", id).Updates(map[string]any{
		"uuid": "current-uuid", "password": "current-secret", "desired_enable": false,
		"desired_minted": true, "last_raw_up_bytes": nil, "last_raw_down_bytes": int64(987654), "last_counter_epoch": uint64(12345),
	}).Error; err != nil {
		t.Fatal(err)
	}
	latest := assertRead(7, 17)
	if latest[0].UUID != "current-uuid" || latest[0].Password != "current-secret" || latest[0].DesiredEnable || !latest[0].DesiredMinted || latest[0].LastRawUpBytes != 0 || latest[0].LastRawDownBytes != 987654 || latest[0].LastCounterEpoch != 12345 {
		t.Fatal("panel read reused stale definitions, NULL values or accounting baselines")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := r.ListByUserAndPanel(ctx, 7, 17); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("panel read hid cancellation behind a prior result")
	}
	wantErr := errors.New("owned panel read failure")
	const failure = "user-panel-query-failure"
	if err := r.db.Callback().Query().Before("gorm:query").Register(failure, func(tx *gorm.DB) { tx.AddError(wantErr) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove(failure) })
	if got, err := r.ListByUserAndPanel(t.Context(), 7, 17); !errors.Is(err, wantErr) || got != nil {
		t.Fatal("panel read converted a repository error to an empty or cached success")
	}
}
