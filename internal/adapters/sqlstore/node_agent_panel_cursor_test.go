package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNodeAgentPanelCursorPreservesFullCurrentLookupAndFailures(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if connection, err := db.DB(); err == nil {
			_ = connection.Close()
		}
	})
	repo := NewRepos(db).NodeAgent
	clock := time.Date(2026, 10, 16, 1, 2, 3, 123000000, time.UTC)
	zero, private := 0, "private-ciphertext-never-selected"
	rows := []nodeAgentRow{
		{ID: 4101, AgentID: "panel-cursor-one", PanelID: 4202, Epoch: 1<<53 + 17,
			CredentialSHA256: strings.Repeat("ab", 32), CredentialCiphertext: &private,
			ObservedProtocolVersion: 3, ObservedCapabilities: jsonStrings{"one", "two"}, ProtocolObservedAt: &clock,
			DesiredCoreEngine: "sing-box", DesiredCoreVersion: "1.14.0", AllowRestrictedReality: true, ObservedCoreEngine: "xray", LastSeen: &clock,
			RefusedProtocolVersion: &zero, RefusedReason: "fixture", RefusedFirstAt: &clock, RefusedAt: &clock, CreatedAt: clock, UpdatedAt: clock},
		{ID: 4102, AgentID: "panel-cursor-two", PanelID: 4201, Epoch: 1, CredentialSHA256: strings.Repeat("cd", 32), DesiredCoreEngine: "xray"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&nodeAgentRow{}).Where("id = ?", rows[1].ID).UpdateColumns(map[string]any{"observed_capabilities": nil, "created_at": nil, "updated_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&nodeAgentRow{}); err != nil {
		t.Fatal(err)
	}
	var public []string
	for _, column := range statement.Schema.DBNames {
		if column != "credential_ciphertext" {
			public = append(public, column)
		}
	}
	slices.Sort(public)
	queries := 0
	const hook = "current-panel-agent-cursor-contract"
	if err := db.Callback().Row().After("gorm:row").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table != "node_agents" || tx.Error != nil {
			return
		}
		queries++
		cursor, ok := tx.Statement.Dest.(*sql.Rows)
		if !ok {
			t.Error("panel lookup did not use the existing complete cursor")
			return
		}
		columns, err := cursor.Columns()
		columns = slices.Clone(columns)
		slices.Sort(columns)
		limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
		if err != nil || !slices.Equal(columns, public) || !ok || limit.Limit == nil || *limit.Limit != 1 {
			t.Error("panel lookup changed public fields, exposed ciphertext or removed the original single-row bound")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(hook) })
	compare := func(panel int64) *domain.NodeAgent {
		t.Helper()
		var original nodeAgentRow
		originalErr := db.WithContext(t.Context()).Omit("CredentialCiphertext").Where("panel_id = ?", panel).First(&original).Error
		before := queries
		got, err := repo.GetByPanelID(t.Context(), panel)
		if originalErr != nil {
			if !errors.Is(originalErr, gorm.ErrRecordNotFound) || !errors.Is(err, domain.ErrNotFound) || got != nil {
				t.Fatal("panel lookup changed missing-row semantics")
			}
		} else if err != nil || !reflect.DeepEqual(got, rowToNodeAgent(&original)) {
			t.Fatal("panel lookup changed complete fields, ownership, nullable metadata or wide epoch")
		}
		if queries != before+1 {
			t.Fatal("panel lookup added queries or reused a prior row")
		}
		return got
	}
	first := compare(4202)
	compare(4201)
	compare(9999)
	compare(0)
	first.ObservedCapabilities[0] = "caller-only"
	*first.LastSeen = clock.Add(time.Hour)
	*first.RefusedProtocolVersion = 99
	if err := db.Model(&nodeAgentRow{}).Where("id = ?", rows[0].ID).UpdateColumns(map[string]any{
		"epoch": uint64(1<<53 + 19), "observed_capabilities": `["current"]`, "desired_core_version": "current-version", "last_seen": clock.Add(time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	current := compare(4202)
	if current.Epoch != 1<<53+19 || current.DesiredCoreVersion != "current-version" || !slices.Equal(current.ObservedCapabilities, []string{"current"}) || !current.LastSeen.Equal(clock.Add(time.Minute)) || *current.RefusedProtocolVersion != 0 {
		t.Fatal("panel lookup retained caller mutation or stale control metadata")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := repo.GetByPanelID(ctx, 4202); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("panel lookup hid cancellation behind a prior result")
	}
	if err := db.Model(&nodeAgentRow{}).Where("id = ?", rows[0].ID).UpdateColumn("observed_capabilities", `{"invalid":"shape"}`).Error; err != nil {
		t.Fatal(err)
	}
	var original nodeAgentRow
	if err := db.Omit("CredentialCiphertext").Where("panel_id = ?", 4202).First(&original).Error; err == nil {
		t.Fatal("invalid JSON did not fail the original model scanner")
	}
	if got, err := repo.GetByPanelID(t.Context(), 4202); err == nil || got != nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatal("panel lookup hid decoding failure or returned partial state")
	}
	if err := db.Model(&nodeAgentRow{}).Where("id = ?", rows[0].ID).UpdateColumn("observed_capabilities", `["recovered"]`).Error; err != nil {
		t.Fatal(err)
	}
	compare(4202)
	wantErr := errors.New("owned panel cursor failure")
	const failure = "current-panel-agent-cursor-failure"
	if err := db.Callback().Row().Before("gorm:row").Register(failure, func(tx *gorm.DB) { tx.AddError(wantErr) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(failure) })
	if got, err := repo.GetByPanelID(t.Context(), 4202); got != nil || !errors.Is(err, wantErr) {
		t.Fatal("panel lookup converted SQL failure to absence or cached success")
	}
}
