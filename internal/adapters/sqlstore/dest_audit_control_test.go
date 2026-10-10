package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type auditControlView struct {
	mu     sync.Mutex
	states map[int64]domain.DestAuditControl
	calls  int
}

func newAuditControlView() *auditControlView {
	return &auditControlView{states: map[int64]domain.DestAuditControl{}}
}
func (v *auditControlView) update(states []domain.DestAuditControl) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	for _, state := range states {
		v.states[state.PanelID] = state
	}
}
func (v *auditControlView) get(id int64) domain.DestAuditControl {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.states[id]
}

func auditControlFixture(t *testing.T) (ports.Repos, *DestAuditRepo, int64) {
	t.Helper()
	r, b := auditIngestFixture(t, 0)
	repos := NewRepos(r.db)
	return repos, repos.DestAudit.(*DestAuditRepo), b.PanelID
}

func TestDestAuditControlSeedsOnlyNativeFieldsWithoutTracing(t *testing.T) {
	_, r, id := auditControlFixture(t)
	legacy := xuiPanelRow{Kind: string(domain.PanelKind3XUI), Name: "legacy-control", URL: "https://example.test", APIToken: "private-unused-token"}
	if err := r.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	queries := 0
	if err := r.db.Callback().Query().Before("gorm:query").Register("audit_control_projection", func(tx *gorm.DB) {
		queries++
		if tx.Statement.Table != "xui_panels" || !reflect.DeepEqual(tx.Statement.Selects, []string{"id", "kind", "audit_collect", "audit_collect_revision"}) {
			t.Error("cache seed read private panel fields")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("audit_control_projection") })
	v := newAuditControlView()
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); err != nil {
		t.Fatal(err)
	}
	if state := v.get(id); !state.Available || state.Collect != domain.AuditCollectHits || state.Revision != 1 {
		t.Fatalf("current native control%+v", state)
	}
	if len(v.states) != 1 || queries != 1 || len(recorder.queries) != 0 {
		t.Fatal("seed omitted controls, loaded legacy rows or leaked tracing")
	}
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("multiple collection observers accepted")
	}
}

func TestDestAuditControlFailedSeedDoesNotRegisterPartialObserver(t *testing.T) {
	_, r, id := auditControlFixture(t)
	v := newAuditControlView()
	if err := r.WatchDestinationAuditControls(t.Context(), nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("nil observer accepted")
	}
	if err := r.db.Callback().Query().Before("gorm:query").Register("audit_control_seed_fault", func(tx *gorm.DB) { tx.AddError(errors.New("private SQL and credential value")) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("audit_control_seed_fault") })
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); err != errAuditStorage || v.calls != 0 {
		t.Fatal("failed seed exposed driver error or partial cache")
	}
	_ = r.db.Callback().Query().Remove("audit_control_seed_fault")
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); err != nil || !v.get(id).Available {
		t.Fatal("failed seed left observer registered")
	}
}

func TestDestAuditControlMetadataNotifiesAfterCommitWhileGateHeld(t *testing.T) {
	repos, r, id := auditControlFixture(t)
	v := newAuditControlView()
	if err := r.WatchDestinationAuditControls(t.Context(), func(states []domain.DestAuditControl) {
		if v.calls > 0 {
			if r.gates.Len() != 1 {
				t.Error("committed metadata notice is outside panel gate")
			}
			// SQLite has one connection. This query proves the original write
			// transaction has released it before notifying the memory observer.
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var row xuiPanelRow
			if err := r.privateDB(ctx).Select("id", "audit_collect", "audit_collect_revision").First(&row, id).Error; err != nil || string(states[0].Collect) != row.AuditCollect || int64(states[0].Revision) != row.AuditCollectRevision {
				t.Error("observer preceded database commit")
			}
		}
		v.update(states)
	}); err != nil {
		t.Fatal(err)
	}
	writer := repos.XUIPanel.(interface {
		UpdateNativeMetadata(context.Context, int64, *string, *string, *domain.PanelUpdateChannel, *domain.AuditCollect) error
	})
	off := domain.AuditCollectOff
	if err := writer.UpdateNativeMetadata(t.Context(), id, nil, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	if got := v.get(id); !got.Available || got.Collect != off || got.Revision != 2 {
		t.Fatal("setting success did not update current control")
	}
	if err := writer.UpdateNativeMetadata(t.Context(), id, nil, nil, nil, &off); err != nil || v.get(id).Revision != 2 {
		t.Fatal("no-op changed collection generation")
	}
	hits := domain.AuditCollectHits
	if err := writer.UpdateNativeMetadata(t.Context(), id, nil, nil, nil, &hits); err != nil || v.get(id).Revision != 3 {
		t.Fatal("off/on did not advance current cache")
	}
	before := v.get(id)
	if err := r.db.Callback().Update().Before("gorm:update").Register("audit_control_write_fault", func(tx *gorm.DB) { tx.AddError(errors.New("write rolled back")) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Update().Remove("audit_control_write_fault") })
	if err := writer.UpdateNativeMetadata(t.Context(), id, nil, nil, nil, &off); err == nil || v.get(id) != before {
		t.Fatal("rolled-back metadata changed cache")
	}
}

func TestDestAuditControlNativeProvisionAndDeleteNotifyWithoutRestart(t *testing.T) {
	repos, r, _ := auditControlFixture(t)
	v := newAuditControlView()
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); err != nil {
		t.Fatal(err)
	}
	agent := &domain.NodeAgent{AgentID: "agt_control_created", CredentialSHA256: strings.Repeat("b", 64), DesiredCoreVersion: "26.6.27"}
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "created-control", URL: "psp://" + agent.AgentID, AuditCollect: domain.AuditCollectOff}
	if err := repos.NativeAgentProvisioning.Create(t.Context(), panel, agent); err != nil {
		t.Fatal(err)
	}
	if got := v.get(panel.ID); !got.Available || got.Collect != domain.AuditCollectOff || got.Revision != 1 {
		t.Fatal("native creation did not notify current cache")
	}
	if err := repos.NativeAgentProvisioning.DeleteConverged(t.Context(), panel.ID); err != nil {
		t.Fatal(err)
	}
	if v.get(panel.ID).Available {
		t.Fatal("deleted native node remained collectable")
	}
}

func TestDestAuditControlSaveAndDeleteCannotLeaveNativeCacheBehind(t *testing.T) {
	repos, r, _ := auditControlFixture(t)
	v := newAuditControlView()
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); err != nil {
		t.Fatal(err)
	}
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "saved-control", URL: "psp://agt_saved_control"}
	if err := repos.XUIPanel.Save(t.Context(), panel); err != nil || !v.get(panel.ID).Available {
		t.Fatal("saved native row omitted from cache")
	}
	if err := repos.XUIPanel.Delete(t.Context(), panel.ID); err != nil || v.get(panel.ID).Available {
		t.Fatal("panel delete omitted cache invalidation")
	}
}

func TestDestAuditControlConversionNotifiesOnlyOnCommittedBackendChange(t *testing.T) {
	f := newServerMigrationFixture(t)
	v := newAuditControlView()
	if err := f.repos.DestAudit.WatchDestinationAuditControls(t.Context(), v.update); err != nil {
		t.Fatal(err)
	}
	if v.get(f.panelID).Available {
		t.Fatal("legacy conversion source was collectable")
	}
	if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, strings.Repeat("0", 64), f.agent, f.raw); err == nil || v.get(f.panelID).Available {
		t.Fatal("failed conversion notified collector")
	}
	if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, f.fingerprint(t), f.agent, f.raw); err != nil {
		t.Fatal(err)
	}
	if state := v.get(f.panelID); !state.Available || state.Collect != domain.AuditCollectHits || state.Revision != 1 {
		t.Fatal("converted node did not become collectable without restart")
	}
}

func TestDestAuditControlWarmAndCommittedWriterCannotDeadlockOrLoseNewState(t *testing.T) {
	repos, r, id := auditControlFixture(t)
	v := newAuditControlView()
	seeded, release := make(chan struct{}), make(chan struct{})
	var seedOnce, releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	warmDone := make(chan error, 1)
	go func() {
		warmDone <- r.WatchDestinationAuditControls(ctx, func(states []domain.DestAuditControl) {
			v.update(states)
			seedOnce.Do(func() {
				close(seeded)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		})
	}()
	select {
	case <-seeded:
	case <-ctx.Done():
		t.Fatal("seed did not reach memory observer")
	}
	writer := repos.XUIPanel.(interface {
		UpdateNativeMetadata(context.Context, int64, *string, *string, *domain.PanelUpdateChannel, *domain.AuditCollect) error
	})
	writeDone := make(chan error, 1)
	off := domain.AuditCollectOff
	go func() { writeDone <- writer.UpdateNativeMetadata(ctx, id, nil, nil, nil, &off) }()
	for {
		var row xuiPanelRow
		if err := r.privateDB(ctx).Select("id", "audit_collect_revision").First(&row, id).Error; err != nil {
			t.Fatal("writer retained DB connection while waiting for seed observer")
		}
		if row.AuditCollectRevision == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("setting transaction could not commit during cache seed")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-writeDone:
		t.Fatal("setting returned before updating the observer")
	default:
	}
	finish()
	if err := <-warmDone; err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if state := v.get(id); state.Collect != off || state.Revision != 2 {
		t.Fatal("initial seed overwrote committed newer control")
	}
}

type auditDelayedCommitPool struct {
	gorm.ConnPool
	committed, release chan struct{}
}

func (p *auditDelayedCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &auditDelayedCommitTx{ConnPool: tx, commit: tx, ctx: ctx, committed: p.committed, release: p.release}, nil
}

type auditDelayedCommitTx struct {
	gorm.ConnPool
	commit             gorm.TxCommitter
	ctx                context.Context
	committed, release chan struct{}
}

func (tx *auditDelayedCommitTx) Commit() error {
	if err := tx.commit.Commit(); err != nil {
		return err
	}
	close(tx.committed)
	select {
	case <-tx.release:
		return nil
	case <-tx.ctx.Done():
		return tx.ctx.Err()
	}
}
func (tx *auditDelayedCommitTx) Rollback() error { return tx.commit.Rollback() }

func TestDestAuditControlLateCreationNoticeCannotResurrectDeletedNativePanel(t *testing.T) {
	repos, r, _ := auditControlFixture(t)
	v := newAuditControlView()
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	committed, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	creator := *repos.NativeAgentProvisioning.(*nativeAgentProvisioningRepo)
	creator.db = r.db.Session(&gorm.Session{NewDB: true}).WithContext(ctx)
	creator.db.Statement.ConnPool = &auditDelayedCommitPool{ConnPool: creator.db.Statement.ConnPool, committed: committed, release: release}
	agent := &domain.NodeAgent{AgentID: "agt_late_created_control", CredentialSHA256: strings.Repeat("c", 64), DesiredCoreVersion: "26.6.27"}
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "late-created-control", URL: "psp://" + agent.AgentID}
	created := make(chan error, 1)
	go func() { created <- creator.Create(ctx, panel, agent) }()
	select {
	case <-committed:
	case <-ctx.Done():
		t.Fatal("creation did not commit")
	}
	var row xuiPanelRow
	if err := r.privateDB(ctx).Select("id").Where("url = ?", panel.URL).First(&row).Error; err != nil {
		t.Fatal("committed create did not release DB connection")
	}
	if err := repos.NativeAgentProvisioning.DeleteConverged(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	finish()
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if v.get(row.ID).Available {
		t.Fatal("delayed creation notification restored a deleted panel")
	}
}

func TestDestAuditControlUnreadablePostCommitCreationInvalidatesWithoutRetryableCRUD(t *testing.T) {
	repos, r, _ := auditControlFixture(t)
	v := newAuditControlView()
	if err := r.WatchDestinationAuditControls(t.Context(), v.update); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Callback().Query().Before("gorm:query").Register("audit_control_postcommit_read_fault", func(tx *gorm.DB) {
		if tx.Statement.Table == "xui_panels" {
			tx.AddError(errors.New("private SQL and credentials"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("audit_control_postcommit_read_fault") })
	agent := &domain.NodeAgent{AgentID: "agt_unreadable_created_control", CredentialSHA256: strings.Repeat("d", 64), DesiredCoreVersion: "26.6.27"}
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "unreadable-created-control", URL: "psp://" + agent.AgentID}
	if err := repos.NativeAgentProvisioning.Create(t.Context(), panel, agent); err != nil || panel.ID == 0 {
		t.Fatal("postcommit read error encouraged retrying committed create")
	}
	if v.get(panel.ID).Available {
		t.Fatal("unreadable committed control failed open")
	}
}
