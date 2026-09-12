package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const nativeCredentialTestKey = "native-credential-test-encryption-key"

func newNativeCredentialTestRepos(t *testing.T) (ports.Repos, *gorm.DB) {
	t.Helper()
	ConfigureSecretKey(nativeCredentialTestKey)
	t.Cleanup(func() { ConfigureSecretKey("") })
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return NewRepos(db), db
}

func nativeCredentialTestIdentity(t *testing.T, id, raw string) (*domain.XUIPanel, *domain.NodeAgent) {
	t.Helper()
	digest, err := nativeCredentialDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: id, URL: "psp://" + id},
		&domain.NodeAgent{AgentID: id, CredentialSHA256: digest, DesiredCoreVersion: "26.6.27"}
}

func TestNativeCredentialRecoveryIsEncryptedAndAbsentFromOrdinaryAgentReads(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	raw := "pspn_" + strings.Repeat("a", 43)
	panel, agent := nativeCredentialTestIdentity(t, "agt_secret_read", raw)
	if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, raw); err != nil {
		t.Fatal(err)
	}
	var stored nodeAgentRow
	if err := db.Where("panel_id = ?", panel.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CredentialCiphertext == nil || !strings.HasPrefix(*stored.CredentialCiphertext, secretPrefix) || strings.Contains(*stored.CredentialCiphertext, raw) {
		t.Fatal("native recovery credential is not encrypted at rest")
	}
	got, err := NewRepos(db).NativeAgentProvisioning.GetCredential(t.Context(), panel.ID)
	if err != nil || got != raw {
		t.Fatalf("reconstructed repo recovery = (%q,%v)", got, err)
	}
	if field, exists := reflect.TypeOf(domain.NodeAgent{}).FieldByName("CredentialCiphertext"); exists {
		t.Fatalf("private ciphertext entered domain: %s", field.Name)
	}
	const callback = "test:native_ordinary_reads_omit_ciphertext"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agents" && strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "credential_ciphertext") {
			t.Error("ordinary agent query selected the private recovery credential")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	reads := []func() (any, error){
		func() (any, error) { return repos.NodeAgent.List(t.Context()) },
		func() (any, error) { return repos.NodeAgent.ListByPanelIDs(t.Context(), []int64{panel.ID}) },
		func() (any, error) { return repos.NodeAgent.GetByPanelID(t.Context(), panel.ID) },
		func() (any, error) { return repos.NodeAgent.GetByAgentID(t.Context(), agent.AgentID) },
		func() (any, error) { return repos.NodeAgent.GetByCredentialSHA256(t.Context(), agent.CredentialSHA256) },
	}
	for _, read := range reads {
		value, err := read()
		encoded, marshalErr := json.Marshal(value)
		if err != nil || marshalErr != nil || strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), *stored.CredentialCiphertext) {
			t.Fatalf("ordinary agent read exposed recovery data: errors %v, %v", err, marshalErr)
		}
	}
}

func TestNativeCredentialLegacyBackfillNeverRotatesAndRequiresCurrentVerifier(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	raw := "pspn_" + strings.Repeat("b", 43)
	panel, agent := nativeCredentialTestIdentity(t, "agt_secret_legacy", raw)
	if err := repos.NativeAgentProvisioning.Create(t.Context(), panel, agent); err != nil {
		t.Fatal(err)
	}
	before, _ := repos.NodeAgent.GetByAgentID(t.Context(), agent.AgentID)
	beforeStreams, _ := repos.NodeAgent.ListStreams(t.Context(), agent.AgentID)
	if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); !errors.Is(err, domain.ErrNotFound) || got != "" {
		t.Fatalf("digest-only legacy recovery = (%q,%v), want not found", got, err)
	}
	if err := repos.NativeAgentProvisioning.StoreCredential(t.Context(), panel.ID, raw+"x"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("mismatched raw credential backfill = %v", err)
	}
	if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); !errors.Is(err, domain.ErrNotFound) || got != "" {
		t.Fatal("mismatched backfill changed the recovery copy")
	}
	if err := repos.NativeAgentProvisioning.StoreCredential(t.Context(), panel.ID, raw); err != nil {
		t.Fatal(err)
	}
	after, _ := repos.NodeAgent.GetByAgentID(t.Context(), agent.AgentID)
	afterStreams, _ := repos.NodeAgent.ListStreams(t.Context(), agent.AgentID)
	if before.ID != after.ID || before.PanelID != after.PanelID || before.Epoch != after.Epoch || before.CredentialSHA256 != after.CredentialSHA256 || !reflect.DeepEqual(beforeStreams, afterStreams) {
		t.Fatal("legacy backfill altered identity/verifier/stream state")
	}
	if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); err != nil || got != raw {
		t.Fatalf("backfilled raw credential = (%q,%v)", got, err)
	}
	columns, err := db.Migrator().ColumnTypes(&nodeAgentRow{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, column := range columns {
		if column.Name() == "credential_ciphertext" {
			found = true
			if !strings.EqualFold(column.DatabaseTypeName(), "text") {
				t.Fatalf("recovery column type = %s", column.DatabaseTypeName())
			}
			if nullable, ok := column.Nullable(); ok && !nullable {
				t.Fatal("recovery column must preserve nullable legacy state")
			}
			if value, ok := column.DefaultValue(); ok && value != "" {
				t.Fatalf("recovery TEXT must not have a default: %q", value)
			}
		}
	}
	if !found {
		t.Fatal("recovery column was not migrated")
	}
}

func TestNativeCredentialSecretRotationIsAtomicAndPreservesIdentityAndStatistics(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	oldRaw, newRaw := "pspn_"+strings.Repeat("c", 43), "pspn_"+strings.Repeat("d", 43)
	panel, agent := nativeCredentialTestIdentity(t, "agt_secret_rotate", oldRaw)
	if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, oldRaw); err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 9, 11, 12, 0, 0, 123_000_000, time.UTC)
	if err := repos.NodeAgent.TouchLastSeen(t.Context(), agent.AgentID, seen); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"listeners":[]}`)
	if _, _, err := repos.NodeAgent.MintStream(t.Context(), agent.AgentID, domain.NodeAgentStreamConfig, body, seen); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&xuiPanelRow{}).Where("id = ?", panel.ID).Update("xray_version", "26.6.27").Error; err != nil {
		t.Fatal(err)
	}
	statistics := nodeRow{PanelID: panel.ID, InboundID: 1, DisplayName: "existing-node", Region: "JP",
		LifetimeUpBytes: 1234, LifetimeDownBytes: 5678, LifetimeTotalBytes: 6912,
		LastInboundCounterEpoch: 99, LastInboundSeeded: true, LastInboundUpBytes: 1200, LastInboundDownBytes: 5600}
	if err := db.Create(&statistics).Error; err != nil {
		t.Fatal(err)
	}
	before, _ := repos.NodeAgent.GetByAgentID(t.Context(), agent.AgentID)
	streamsBefore, _ := repos.NodeAgent.ListStreams(t.Context(), agent.AgentID)
	rotated, err := repos.NativeAgentProvisioning.RotateCredentialWithSecret(t.Context(), panel.ID, newRaw)
	if err != nil || rotated.ID != before.ID || rotated.PanelID != before.PanelID || rotated.AgentID != before.AgentID || rotated.Epoch != before.Epoch || !rotated.LastSeen.Equal(*before.LastSeen) {
		t.Fatalf("rotation altered identity/liveness = (%+v,%v)", rotated, err)
	}
	streamsAfter, _ := repos.NodeAgent.ListStreams(t.Context(), agent.AgentID)
	if !reflect.DeepEqual(streamsBefore, streamsAfter) {
		t.Fatal("rotation changed desired/applied coordinates")
	}
	var panelAfter xuiPanelRow
	if err := db.First(&panelAfter, panel.ID).Error; err != nil || panelAfter.XrayVersion != "26.6.27" {
		t.Fatal("rotation changed observed server state")
	}
	var statisticsAfter nodeRow
	if err := db.First(&statisticsAfter, statistics.ID).Error; err != nil ||
		statisticsAfter.LifetimeUpBytes != statistics.LifetimeUpBytes || statisticsAfter.LifetimeDownBytes != statistics.LifetimeDownBytes ||
		statisticsAfter.LifetimeTotalBytes != statistics.LifetimeTotalBytes || statisticsAfter.LastInboundCounterEpoch != statistics.LastInboundCounterEpoch ||
		statisticsAfter.LastInboundUpBytes != statistics.LastInboundUpBytes || statisticsAfter.LastInboundDownBytes != statistics.LastInboundDownBytes {
		t.Fatal("credential rotation rewrote stored node statistics/counter baseline")
	}
	if _, err := repos.NodeAgent.GetByCredentialSHA256(t.Context(), before.CredentialSHA256); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old verifier still authenticates: %v", err)
	}
	if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); err != nil || got != newRaw {
		t.Fatalf("rotation left a stale recovery copy = (%q,%v)", got, err)
	}
	if err := repos.NativeAgentProvisioning.StoreCredential(t.Context(), panel.ID, oldRaw); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("old raw credential restored after verifier rotation")
	}
	// The compatible digest-only method must revoke/clear the secret copy,
	// not leave the previous raw value available for future install scripts.
	digest, _ := nativeCredentialDigest(newRaw + "x")
	if _, err := repos.NativeAgentProvisioning.RotateCredential(t.Context(), panel.ID, digest); err != nil {
		t.Fatal(err)
	}
	if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); !errors.Is(err, domain.ErrNotFound) || got != "" {
		t.Fatalf("digest-only rotation retained recovery copy = (%q,%v)", got, err)
	}
}

func TestNativeCredentialNeverFallsBackToPlaintextAndRejectsCorruptRecovery(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	raw := "enc:v1:" + strings.Repeat("e", 43) // Valid bearer prefix must not bypass encryption.
	panel, agent := nativeCredentialTestIdentity(t, "agt_secret_corrupt", raw)
	ConfigureSecretKey("")
	if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, raw); err == nil || panel.ID != 0 || agent.ID != 0 {
		t.Fatal("no-key create admitted plaintext or partial identity")
	}
	ConfigureSecretKey(nativeCredentialTestKey)
	if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, raw); err != nil {
		t.Fatal(err)
	}
	var stored nodeAgentRow
	if err := db.Where("panel_id = ?", panel.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CredentialCiphertext == nil || *stored.CredentialCiphertext == raw {
		t.Fatal("raw enc:v1: credential passed through encryption")
	}
	for _, key := range []string{"", "incorrect-encryption-key"} {
		ConfigureSecretKey(key)
		if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); err == nil || got != "" || strings.Contains(err.Error(), raw) || strings.Contains(err.Error(), *stored.CredentialCiphertext) {
			t.Fatalf("missing/wrong-key recovery = (%q,%v)", got, err)
		}
		if err := repos.NativeAgentProvisioning.StoreCredential(t.Context(), panel.ID, raw); key == "" && err == nil {
			t.Fatal("no-key backfill admitted plaintext")
		}
	}
	ConfigureSecretKey(nativeCredentialTestKey)
	otherCiphertext, err := encryptNativeCredential(raw + "x")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", raw, "private-ciphertext-marker", secretPrefix + "broken-private-marker", otherCiphertext} {
		if err := db.Model(&nodeAgentRow{}).Where("panel_id = ?", panel.ID).Update("credential_ciphertext", bad).Error; err != nil {
			t.Fatal(err)
		}
		if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); err == nil || errors.Is(err, domain.ErrNotFound) || got != "" || strings.Contains(err.Error(), raw) || strings.Contains(err.Error(), "private-marker") {
			t.Fatalf("corrupt recovery silently accepted/fell back = (%q,%v)", got, err)
		}
	}
}

func TestNativeCredentialSecretWritesFailAtomically(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	raw := "pspn_" + strings.Repeat("f", 43)
	panel, agent := nativeCredentialTestIdentity(t, "agt_secret_atomic", raw)
	const callback = "test:reject_native_secret_update"
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agents" {
			tx.AddError(errors.New("forced DB failure with private driver diagnostics"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, raw); err == nil || panel.ID != 0 || agent.ID != 0 || strings.Contains(err.Error(), "private driver") {
		t.Fatalf("failed secret create exposed detail/partial identity: %v", err)
	}
	for _, model := range []any{&xuiPanelRow{}, &nodeAgentRow{}, &nodeAgentStreamRow{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("secret create failed but left %T rows %d: %v", model, count, err)
		}
	}
	if err := db.Callback().Update().Remove(callback); err != nil {
		t.Fatal(err)
	}
	if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, raw); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agents" {
			tx.AddError(errors.New("forced DB failure with private driver diagnostics"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	if _, err := repos.NativeAgentProvisioning.RotateCredentialWithSecret(t.Context(), panel.ID, raw+"x"); err == nil || strings.Contains(err.Error(), "private driver") {
		t.Fatalf("failed rotation did not hide private DB diagnostics: %v", err)
	}
	if got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID); err != nil || got != raw {
		t.Fatal("failed rotation altered verifier/recovery pair")
	}
}

func TestNativeCredentialStoreAndRotateCannotResurrectOldCredential(t *testing.T) {
	for attempt := 0; attempt < 12; attempt++ {
		t.Run(fmt.Sprintf("race_%02d", attempt), func(t *testing.T) {
			repos, _ := newNativeCredentialTestRepos(t)
			oldRaw, newRaw := "pspn_"+strings.Repeat("g", 43), "pspn_"+strings.Repeat("h", 43)
			panel, agent := nativeCredentialTestIdentity(t, "agt_secret_race", oldRaw)
			if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, oldRaw); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for _, operation := range []func(context.Context) error{
				func(ctx context.Context) error {
					return repos.NativeAgentProvisioning.StoreCredential(ctx, panel.ID, oldRaw)
				},
				func(ctx context.Context) error {
					_, err := repos.NativeAgentProvisioning.RotateCredentialWithSecret(ctx, panel.ID, newRaw)
					return err
				},
			} {
				wg.Add(1)
				go func(operation func(context.Context) error) {
					defer wg.Done()
					<-start
					results <- operation(t.Context())
				}(operation)
			}
			close(start)
			wg.Wait()
			close(results)
			for err := range results {
				if err != nil && !errors.Is(err, domain.ErrConflict) {
					t.Fatalf("store/rotation produced an unexpected failure: %v", err)
				}
			}
			got, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID)
			if err != nil || got != newRaw {
				t.Fatalf("old credential survived concurrent store/rotation = (%q,%v)", got, err)
			}
		})
	}
}

func TestNativeCredentialRejectsInvalidWireShapeBeforeWrites(t *testing.T) {
	repos, _ := newNativeCredentialTestRepos(t)
	valid := "pspn_" + strings.Repeat("i", 43)
	panel, agent := nativeCredentialTestIdentity(t, "agt_secret_invalid", valid)
	for _, raw := range []string{"", strings.Repeat("a", nodeprotocol.MinNodeCredentialBytes-1), strings.Repeat("a", nodeprotocol.MaxNodeCredentialBytes+1), valid + "\n", valid + " ", valid + "\x00", valid + "中"} {
		if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, raw); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid raw credential accepted: %v", err)
		}
	}
	if panel.ID != 0 || agent.ID != 0 {
		t.Fatal("invalid credentials partially created identity")
	}
}

func TestNativeCredentialSecretOperationsNeverTracePrivateSQLValues(t *testing.T) {
	_, db := newNativeCredentialTestRepos(t)
	spy := &nodeAgentTaskSQLSpy{}
	repo := &nativeAgentProvisioningRepo{db: db.Session(&gorm.Session{Logger: spy})}
	raw := "pspn_" + strings.Repeat("private-credential-do-not-log", 2)
	panel, agent := nativeCredentialTestIdentity(t, "agt_secret_logs", raw)
	if err := repo.CreateWithCredential(t.Context(), panel, agent, raw); err != nil {
		t.Fatal(err)
	}
	if err := repo.StoreCredential(t.Context(), panel.ID, raw); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetCredential(t.Context(), panel.ID); err != nil || got != raw {
		t.Fatal("credential recovery failed")
	}
	if _, err := repo.RotateCredentialWithSecret(t.Context(), panel.ID, raw+"x"); err != nil {
		t.Fatal(err)
	}
	const callback = "test:private_native_credential_driver_error"
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agents" {
			tx.AddError(fmt.Errorf("forced private SQL driver diagnostics %s", raw))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	if _, err := repo.RotateCredentialWithSecret(t.Context(), panel.ID, raw+"y"); err == nil || strings.Contains(err.Error(), raw) {
		t.Fatalf("private driver diagnostics escaped storage boundary: %v", err)
	}
	if len(spy.messages) != 0 {
		t.Fatalf("secret operations traced %d SQL/error messages", len(spy.messages))
	}
}
