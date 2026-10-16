package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCurrentNodeAgentCursorReusesDestinationsWithoutSharingNullableRows(t *testing.T) {
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
	clock := time.Date(2026, 10, 16, 1, 2, 3, 123000000, time.UTC)
	for i := range 4 {
		at, version := clock.Add(time.Duration(i)*time.Minute), i
		private := "private-ciphertext-excluded"
		model := nodeAgentRow{
			ID: int64(9501 + i), AgentID: fmt.Sprintf("scalar-cursor-%d", i), PanelID: int64(9604 - i),
			Epoch: uint64(1<<53 + 17 + i), CredentialSHA256: strings.Repeat(fmt.Sprintf("%02x", i+1), 32), CredentialCiphertext: &private,
			ObservedProtocolVersion: 3, ObservedCapabilities: jsonStrings{fmt.Sprintf("capability-%d", i)}, ProtocolObservedAt: &at,
			DesiredCoreEngine: "xray", DesiredCoreVersion: fmt.Sprintf("version-%d", i), AllowRestrictedReality: i%2 == 0, ObservedCoreEngine: "xray",
			LastSeen: &at, RefusedProtocolVersion: &version, RefusedReason: fmt.Sprintf("reason-%d", i), RefusedFirstAt: &at, RefusedAt: &at,
			CreatedAt: at, UpdatedAt: at,
		}
		if err := db.Create(&model).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Exercise non-NULL -> different non-NULL -> NULL -> non-NULL in one
	// cursor. Reusing a pointer from either earlier row would change its value.
	if err := db.Model(&nodeAgentRow{}).Where("id = ?", 9503).UpdateColumns(map[string]any{
		"observed_capabilities": nil, "protocol_observed_at": nil, "last_seen": nil,
		"refused_protocol_version": nil, "refused_first_at": nil, "refused_at": nil, "created_at": nil, "updated_at": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var original []nodeAgentRow
	if err := db.Omit("CredentialCiphertext").Order("id").Find(&original).Error; err != nil {
		t.Fatal(err)
	}
	want := make([]*domain.NodeAgent, len(original))
	for i := range original {
		want[i] = rowToNodeAgent(&original[i])
	}
	repo := NewRepos(db).NodeAgent
	got, err := repo.List(t.Context())
	if err != nil || len(got) != 4 || !reflect.DeepEqual(got, want) {
		t.Fatal("reused node-agent destinations changed complete rows, nullable state or wide epochs")
	}
	if got[0].LastSeen == got[1].LastSeen || got[0].ProtocolObservedAt == got[1].ProtocolObservedAt || got[0].RefusedProtocolVersion == got[1].RefusedProtocolVersion || got[0].RefusedAt == got[1].RefusedAt || got[0].RefusedFirstAt == got[1].RefusedFirstAt {
		t.Fatal("node-agent cursor shared nullable storage between returned rows")
	}
	*got[0].LastSeen = clock.Add(time.Hour)
	*got[0].ProtocolObservedAt = clock.Add(time.Hour)
	*got[0].RefusedProtocolVersion = 99
	*got[0].RefusedFirstAt = clock.Add(time.Hour)
	*got[0].RefusedAt = clock.Add(time.Hour)
	got[0].ObservedCapabilities[0] = "caller-only"
	if !reflect.DeepEqual(got[1:], want[1:]) {
		t.Fatal("mutating an earlier node-agent result changed a later row")
	}
	byPanel, err := repo.ListByPanelIDs(t.Context(), []int64{9601, 9602, 9603, 9604})
	if err != nil || !reflect.DeepEqual(byPanel, []*domain.NodeAgent{want[3], want[2], want[1], want[0]}) {
		t.Fatal("panel-scoped cursor retained mutations or changed ordering and nullable states")
	}
	for i := range want {
		one, err := repo.GetByPanelID(t.Context(), want[i].PanelID)
		if err != nil || !reflect.DeepEqual(one, want[i]) {
			t.Fatal("single-panel cursor changed complete current scalar, JSON or nullable values")
		}
	}
}

func TestCurrentNodeAgentCursorMatchesCompleteModelsAndOwnedCurrentRows(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgent
	if empty, err := repo.List(t.Context()); err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("empty agent list changed", err)
	}
	clock := time.Date(2026, 10, 15, 1, 2, 3, 123000000, time.UTC)
	zero := 0
	private := "private encrypted credential"
	models := []nodeAgentRow{
		{ID: 9101, AgentID: "cursor-agent-one", PanelID: 9202, Epoch: 1<<53 + 7, CredentialSHA256: strings.Repeat("ab", 32), CredentialCiphertext: &private,
			ObservedProtocolVersion: 3, ObservedCapabilities: jsonStrings{"one", "two"}, ProtocolObservedAt: &clock,
			DesiredCoreEngine: "sing-box", DesiredCoreVersion: "1.14.0", AllowRestrictedReality: true, ObservedCoreEngine: "xray", LastSeen: &clock,
			RefusedProtocolVersion: &zero, RefusedReason: "fixture", RefusedFirstAt: &clock, RefusedAt: &clock, CreatedAt: clock, UpdatedAt: clock},
		{ID: 9102, AgentID: "cursor-agent-two", PanelID: 9201, Epoch: 1, CredentialSHA256: strings.Repeat("cd", 32), DesiredCoreEngine: "xray", CreatedAt: clock, UpdatedAt: clock},
	}
	if err := db.Create(&models).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&nodeAgentRow{}).Where("id = ?", models[1].ID).UpdateColumns(map[string]any{"observed_capabilities": nil, "created_at": nil, "updated_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	var full []nodeAgentRow
	if err := db.Omit("CredentialCiphertext").Order("id").Find(&full).Error; err != nil {
		t.Fatal(err)
	}
	want := make([]*domain.NodeAgent, len(full))
	for i := range full {
		want[i] = rowToNodeAgent(&full[i])
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&nodeAgentRow{}); err != nil {
		t.Fatal(err)
	}
	var columns []string
	for _, name := range statement.Schema.DBNames {
		if name != "credential_ciphertext" {
			columns = append(columns, name)
		}
	}
	slices.Sort(columns)
	queries := 0
	const hook = "current-node-agent-cursor-shape"
	if err := db.Callback().Row().After("gorm:row").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table != "node_agents" || tx.Error != nil {
			return
		}
		queries++
		rows, ok := tx.Statement.Dest.(*sql.Rows)
		if !ok {
			t.Error("agent list did not use a cursor")
			return
		}
		got, err := rows.Columns()
		if err != nil {
			t.Error(err)
			return
		}
		got = slices.Clone(got)
		slices.Sort(got)
		if !slices.Equal(got, columns) {
			t.Error("agent list changed complete public projection or selected private ciphertext")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(hook) })
	got, err := repo.List(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("agent list changed complete model values or ID ordering", err)
	}
	byPanel, err := repo.ListByPanelIDs(t.Context(), []int64{9202, 9201, 9202, 9999})
	if err != nil || !reflect.DeepEqual(byPanel, []*domain.NodeAgent{want[1], want[0]}) {
		t.Fatal("panel-scoped list changed values, deduplication or panel ordering", err)
	}
	if queries != 2 {
		t.Fatal("agent lists did not each read current SQL")
	}
	got[0].ObservedCapabilities[0] = "mutated"
	*got[0].ProtocolObservedAt = clock.Add(time.Hour)
	*got[0].RefusedProtocolVersion = 99
	if !reflect.DeepEqual(byPanel, []*domain.NodeAgent{want[1], want[0]}) {
		t.Fatal("separate agent reads shared returned state")
	}
	if err := db.Model(&nodeAgentRow{}).Where("id = ?", models[0].ID).UpdateColumns(map[string]any{"epoch": 1<<53 + 9, "desired_core_version": "current-version", "observed_capabilities": `["current"]`, "last_seen": clock.Add(time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	current, err := repo.List(t.Context())
	if err != nil || len(current) != 2 || current[0].Epoch != 1<<53+9 || current[0].DesiredCoreVersion != "current-version" || !slices.Equal(current[0].ObservedCapabilities, []string{"current"}) || current[0].LastSeen == nil || !current[0].LastSeen.Equal(clock.Add(time.Minute)) {
		t.Fatal("agent list reused a prior row or truncated its epoch", err)
	}
	if empty, err := repo.ListByPanelIDs(t.Context(), nil); err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("empty panel filter changed", err)
	}
	if empty, err := repo.ListByPanelIDs(t.Context(), []int64{9999}); err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("missing panel list changed", err)
	}
}

func TestCurrentNodeAgentCursorRejectsDecodeAndContextFailuresWithoutPartialResults(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgent
	for i, id := range []string{"cursor-good", "cursor-corrupt"} {
		if err := db.Create(&nodeAgentRow{AgentID: id, PanelID: int64(9301 + i), Epoch: 1, CredentialSHA256: strings.Repeat(string(rune('a'+i)), 64), DesiredCoreEngine: "xray", ObservedCapabilities: jsonStrings{"current"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&nodeAgentRow{}).Where("agent_id = ?", "cursor-corrupt").UpdateColumn("observed_capabilities", `{"invalid":"shape"}`).Error; err != nil {
		t.Fatal(err)
	}
	var original []nodeAgentRow
	if err := db.Omit("CredentialCiphertext").Order("id").Find(&original).Error; err == nil {
		t.Fatal("corrupt JSON fixture did not fail original model scanner")
	}
	for _, read := range []func(context.Context) ([]*domain.NodeAgent, error){repo.List, func(ctx context.Context) ([]*domain.NodeAgent, error) {
		return repo.ListByPanelIDs(ctx, []int64{9301, 9302})
	}} {
		if got, err := read(t.Context()); err == nil || got != nil {
			t.Fatal("failed agent read returned partial decoded rows")
		}
	}
	if err := db.Model(&nodeAgentRow{}).Where("agent_id = ?", "cursor-corrupt").UpdateColumn("observed_capabilities", `["recovered"]`).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := repo.List(ctx); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("canceled agent cursor returned data or lost cancellation", err)
	}
	if got, err := repo.List(t.Context()); err != nil || len(got) != 2 || !slices.Equal(got[1].ObservedCapabilities, []string{"recovered"}) {
		t.Fatal("agent cursor did not recover after a failed read", err)
	}
}
