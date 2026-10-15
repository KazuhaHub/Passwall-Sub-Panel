package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestNativeDesiredClientSnapshotBoundsReadsAndPreservesExactIdentity(t *testing.T) {
	clients, _ := inboundBatchFixture(t, 601)
	const email = "target@fixture.local"
	var rows []pspClientRow
	for i := 1; i <= 601; i++ {
		name := fmt.Sprintf("client-%d@fixture.local", i)
		if i == 1 {
			name = email
		}
		rows = append(rows, pspClientRow{ID: int64(i), UserID: int64(i), PanelID: 7, Email: name})
	}
	rows = append(rows, pspClientRow{ID: 602, UserID: 602, PanelID: 8, Email: email}, pspClientRow{ID: 603, UserID: 603, PanelID: 7, Email: email}, pspClientRow{ID: 604, UserID: 604, PanelID: 7, Email: strings.ToUpper(email)})
	if err := clients.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := clients.db.Create(&pspClientInboundRow{ClientID: 603, NodeID: 7, FlowOverride: "duplicate-flow", State: "applied", AppliedVersion: 19}).Error; err != nil {
		t.Fatal(err)
	}
	r := &nativeDesiredRepo{db: clients.db}
	full, err := r.Load(t.Context(), 7)
	if err != nil || len(full.Clients) != 603 {
		t.Fatal("fixture does not have its full native client set")
	}
	queries, clientQueries, attachmentQueries, outsideTransaction := 0, 0, 0, false
	hook := "native-client-scoped-query-shape"
	if err := r.db.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		queries++
		_, transaction := tx.Statement.ConnPool.(gorm.TxCommitter)
		outsideTransaction = outsideTransaction || !transaction
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Callback().Row().After("gorm:row").Register(hook, func(tx *gorm.DB) {
		queries++
		_, transaction := tx.Statement.ConnPool.(gorm.TxCommitter)
		outsideTransaction = outsideTransaction || !transaction
		switch tx.Statement.Table {
		case "psp_clients":
			if !strings.Contains(tx.Statement.SQL.String(), "panel_id = ") || !strings.Contains(tx.Statement.SQL.String(), "email = ") || len(tx.Statement.Vars) != 2 || tx.Statement.Vars[0] != int64(7) || tx.Statement.Vars[1] != email {
				t.Error("client cursor lost its bound owner and email filters")
			}
			clientQueries++
		case "psp_client_inbounds":
			if !strings.Contains(tx.Statement.SQL.String(), "client_id IN ") || !reflect.DeepEqual(tx.Statement.Vars, []any{int64(1), int64(603)}) {
				t.Error("attachment cursor lost its exact current client closure")
			}
			attachmentQueries++
		default:
			t.Error("scoped snapshot opened an unexpected cursor")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove(hook); _ = r.db.Callback().Row().Remove(hook) })
	scoped, err := r.LoadClient(t.Context(), 7, email)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 3 || clientQueries != 1 || attachmentQueries != 1 || outsideTransaction {
		t.Fatal("single client read scanned the fleet, crossed its owner or escaped the snapshot transaction")
	}
	_ = r.db.Callback().Query().Remove(hook)
	_ = r.db.Callback().Row().Remove(hook)
	var expected []ports.NativeDesiredClient
	for _, c := range full.Clients {
		if c.Client.Email == email {
			expected = append(expected, c)
		}
	}
	if !reflect.DeepEqual(scoped.Nodes, full.Nodes) || !reflect.DeepEqual(scoped.Clients, expected) || len(scoped.Clients) != 2 {
		t.Fatal("scoped lookup changed duplicate order, byte identity, credentials or attachments")
	}
	missing, err := r.LoadClient(t.Context(), 8, "absent@fixture.local")
	if err != nil || len(missing.Clients) != 0 {
		t.Fatal("scoped missing client became an unrelated observed row")
	}
	if _, err := r.LoadClient(t.Context(), 0, email); err == nil {
		t.Fatal("scoped lookup accepted no owner")
	}
	if _, err := r.LoadClient(t.Context(), 7, ""); err == nil {
		t.Fatal("empty lookup became an unbounded read")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.LoadClient(ctx, 7, email); !errors.Is(err, context.Canceled) {
		t.Fatal("scoped lookup lost cancellation")
	}
}

func TestNativeDesiredClientSnapshotDoesNotLogLookupEmail(t *testing.T) {
	clients, _ := inboundBatchFixture(t, 1)
	r := &nativeDesiredRepo{db: clients.db.Session(&gorm.Session{Logger: newGORMLogger().LogMode(logger.Info)})}
	const email = "private-lookup-marker@fixture.invalid"
	var readErr error
	logged := captureLog(t, func() { _, readErr = r.LoadClient(t.Context(), 7, email) })
	if readErr != nil || !strings.Contains(logged, "psp_clients") || strings.Contains(logged, email) || !strings.Contains(logged, "?") && !strings.Contains(logged, "$1") {
		t.Fatal("scoped lookup exposed its email or lost useful parameterized SQL")
	}
	control := captureLog(t, func() {
		var rows []pspClientRow
		readErr = r.db.Where("panel_id = ? AND email = ?", 7, email).Find(&rows).Error
	})
	if readErr != nil || !strings.Contains(control, email) {
		t.Fatal("log capture did not detect the unwrapped lookup value")
	}
}
