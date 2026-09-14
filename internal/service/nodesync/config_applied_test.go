package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type configAppliedFixture struct {
	db            *gorm.DB
	repos         *ports.Repos
	service       *Service
	agent         *domain.NodeAgent
	node          *domain.Node
	now           time.Time
	invalidations int
}

func newConfigAppliedFixture(t *testing.T) *configAppliedFixture {
	t.Helper()
	sqlstore.ConfigureSecretKey("test-only-native-config-ack-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "config-applied.db"))
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	captured, pending, checked := now.Add(-time.Hour), now.Add(-30*time.Minute), now.Add(-time.Minute)
	node := &domain.Node{
		PanelID: 9, InboundID: 1, DisplayName: "config-ack", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 444, ObservedProtocol: "vless", ObservedPort: 443,
		InboundListen: "0.0.0.0", InboundRemark: "managed listener", Enabled: true, Region: "CA",
		InboundSettings: `{"decryption":"none","fixture_secret":"keep-settings"}`,
		StreamSettings:  `{"network":"tcp","fixture_secret":"keep-stream"}`,
		Sniffing:        `{"enabled":true}`, Allocate: `{"strategy":"always"}`, InboundExpiryTime: 1893456000000,
		ConfigSyncedAt: &captured, ConfigPendingSince: &pending, ConfigSyncState: domain.ConfigSyncPending,
		HealthState: domain.NodeHealthState("unreachable"), HealthCheckedAt: &checked, HealthDetail: "fixture probe timeout",
		LifetimeUpBytes: 123, LifetimeDownBytes: 456, LifetimeTotalBytes: 579,
		LastInboundUpBytes: 12, LastInboundDownBytes: 34, LastInboundTotalBytes: 46,
		LastInboundCounterEpoch: 3, LastInboundSeeded: true,
	}
	if err := repos.Node.Create(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	agent := &domain.NodeAgent{
		AgentID: "agt_config_ack", PanelID: node.PanelID, Epoch: 7,
		CredentialSHA256: strings.Repeat("a", 64),
	}
	if err := repos.NodeAgent.Create(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	f := &configAppliedFixture{db: db, repos: &repos, agent: agent, node: node, now: now}
	f.service, err = New(Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask,
		Users: repos.User, Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
		Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.SetRenderInvalidator(func() { f.invalidations++ })
	return f
}

func (f *configAppliedFixture) storedNode(t *testing.T) *domain.Node {
	t.Helper()
	node, err := f.repos.Node.GetByID(t.Context(), f.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func (f *configAppliedFixture) mint(t *testing.T) nodeprotocol.SyncResponse {
	t.Helper()
	response, err := f.service.Sync(t.Context(), nodeprotocol.NodeReport{
		AgentID: f.agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Config.Body == nil || len(response.Config.Body.Listeners) != 1 {
		t.Fatal("fixture did not mint the managed listener")
	}
	return response
}

func (f *configAppliedFixture) receipt(response nodeprotocol.SyncResponse) nodeprotocol.NodeReport {
	have := emptyProtocolHave()
	have[nodeprotocol.StreamConfig] = nodeprotocol.StreamState{Applied: response.Config.Version, ETag: response.Config.ETag}
	return nodeprotocol.NodeReport{
		AgentID: f.agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: have,
		Objects: []nodeprotocol.ObjectStatus{{
			Stream: nodeprotocol.StreamConfig, Key: string(nodeprotocol.NewListenerKey(f.node.ID)),
			State: nodeprotocol.ObjectApplied, SinceVersion: response.Config.Version,
		}},
	}
}

func (f *configAppliedFixture) encryptedConfig(t *testing.T) [2]string {
	t.Helper()
	var row struct{ InboundSettings, StreamSettings string }
	if err := f.db.Table("nodes").Select("inbound_settings", "stream_settings").Where("id = ?", f.node.ID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(row.InboundSettings, "enc:v1:") || !strings.HasPrefix(row.StreamSettings, "enc:v1:") {
		t.Fatal("fixture secrets must be encrypted at rest")
	}
	return [2]string{row.InboundSettings, row.StreamSettings}
}

func assertConfigAckOnly(t *testing.T, before, after *domain.Node, confirmed bool) {
	t.Helper()
	want := *before
	if confirmed {
		want.ObservedProtocol, want.ObservedPort = before.DesiredProtocol, before.DesiredPort
		want.ConfigSyncState, want.ConfigPendingSince = domain.ConfigSyncSynced, nil
	}
	if !reflect.DeepEqual(&want, after) {
		t.Fatal("receipt changed unexpected node fields or failed to confirm the exact current configuration")
	}
}

func TestNativeConfigAppliedConfirmsPendingWithoutChangingSnapshot(t *testing.T) {
	f := newConfigAppliedFixture(t)
	response := f.mint(t)
	before, encrypted := f.storedNode(t), f.encryptedConfig(t)
	if before.ConfigSyncState != domain.ConfigSyncPending || before.ConfigPendingSince == nil {
		t.Fatal("fixture must start pending")
	}
	report := f.receipt(response)
	if _, err := f.service.Sync(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	after := f.storedNode(t)
	assertConfigAckOnly(t, before, after, true)
	if f.encryptedConfig(t) != encrypted {
		t.Fatal("ACK rewrote encrypted desired secrets")
	}
	if f.invalidations != 1 {
		t.Fatalf("first confirmed receipt invalidations = %d, want 1", f.invalidations)
	}
	// A replay is expected after a lost HTTP response. It must not restamp the
	// snapshot, rewrite secrets, or continuously discard subscription caches.
	f.now = f.now.Add(time.Minute)
	if _, err := f.service.Sync(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	assertConfigAckOnly(t, after, f.storedNode(t), false)
	if f.encryptedConfig(t) != encrypted || f.invalidations != 1 {
		t.Fatal("duplicate ACK rewrote desired secrets or invalidated an already-confirmed configuration")
	}
}

func TestNativeConfigAppliedConfirmsPendingWhenEndpointAlreadyMatches(t *testing.T) {
	f := newConfigAppliedFixture(t)
	if err := f.repos.Node.UpdateObservedEndpoint(t.Context(), f.node.ID, domain.NodeObservedEndpoint{
		Protocol: f.node.DesiredProtocol, Port: f.node.DesiredPort,
	}); err != nil {
		t.Fatal(err)
	}
	response := f.mint(t)
	before := f.storedNode(t)
	if _, err := f.service.Sync(t.Context(), f.receipt(response)); err != nil {
		t.Fatal(err)
	}
	assertConfigAckOnly(t, before, f.storedNode(t), true)
	if f.invalidations != 1 {
		t.Fatal("matching endpoint must still confirm pending config and invalidate once")
	}
}

func TestNativeConfigAppliedIgnoresNonConfirmingReceipts(t *testing.T) {
	for _, name := range []string{
		"partial", "rejected", "missing_listener", "stale_minted_config", "old_epoch",
		"future_epoch", "future_version", "current_version_wrong_etag",
	} {
		t.Run(name, func(t *testing.T) {
			f := newConfigAppliedFixture(t)
			response := f.mint(t)
			report := f.receipt(response)
			wantError := ""
			switch name {
			case "partial":
				report.Partial, report.Objects = true, nil
			case "rejected":
				report.Objects[0].State = nodeprotocol.ObjectRejected
				report.Objects[0].FirstFailedAtMS, report.Objects[0].IssueCode = f.now.UnixMilli(), "fixture_rejection"
			case "missing_listener":
				report.Objects = nil
			case "stale_minted_config":
				updated := f.storedNode(t)
				updated.InboundSettings = `{"decryption":"none","fixture_secret":"new-desired"}`
				if err := f.repos.Node.UpdateInboundConfig(t.Context(), updated); err != nil {
					t.Fatal(err)
				}
				newResponse := f.mint(t)
				if newResponse.Config.Version.Version <= response.Config.Version.Version || newResponse.Config.ETag == response.Config.ETag {
					t.Fatal("new desired configuration did not mint a distinct newer version")
				}
			case "old_epoch", "future_epoch", "future_version", "current_version_wrong_etag":
				have := report.Have[nodeprotocol.StreamConfig]
				switch name {
				case "old_epoch":
					have.Applied.Epoch--
				case "future_epoch":
					have.Applied.Epoch++
					wantError = "exceeds desired epoch"
				case "future_version":
					have.Applied.Version++
					wantError = "exceeds desired version"
				case "current_version_wrong_etag":
					have.ETag = nodeprotocol.ETag(strings.Repeat("b", 64))
					wantError = "etag conflicts"
				}
				report.Have[nodeprotocol.StreamConfig] = have
			}
			before, encrypted := f.storedNode(t), f.encryptedConfig(t)
			_, err := f.service.Sync(t.Context(), report)
			if wantError == "" && err != nil {
				t.Fatal(err)
			}
			if wantError != "" && (err == nil || !strings.Contains(err.Error(), wantError)) {
				t.Fatalf("invalid receipt error = %v, want %q", err, wantError)
			}
			assertConfigAckOnly(t, before, f.storedNode(t), false)
			if f.encryptedConfig(t) != encrypted || f.invalidations != 0 {
				t.Fatal("non-confirming receipt rewrote desired secrets or invalidated render cache")
			}
		})
	}
}

func TestNativeConfigAppliedAcceptsOlderVersionWithCurrentABAContent(t *testing.T) {
	f := newConfigAppliedFixture(t)
	originalResponse := f.mint(t)
	original := f.storedNode(t)
	changed := *original
	changed.InboundSettings = `{"decryption":"none","fixture_secret":"intermediate-B"}`
	if err := f.repos.Node.UpdateInboundConfig(t.Context(), &changed); err != nil {
		t.Fatal(err)
	}
	intermediate := f.mint(t)
	if err := f.repos.Node.UpdateInboundConfig(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	current := f.mint(t)
	if intermediate.Config.ETag == originalResponse.Config.ETag || current.Config.ETag != originalResponse.Config.ETag ||
		current.Config.Version.Version <= intermediate.Config.Version.Version {
		t.Fatal("fixture must mint A/B/A with the original content digest and a newer current version")
	}
	before, encrypted := f.storedNode(t), f.encryptedConfig(t)
	// The node still runs the first A. Matching the exact current A digest is
	// sufficient despite its older minted version; requiring the newest number
	// would break the protocol's existing content-based convergence contract.
	if _, err := f.service.Sync(t.Context(), f.receipt(originalResponse)); err != nil {
		t.Fatal(err)
	}
	assertConfigAckOnly(t, before, f.storedNode(t), true)
	if f.encryptedConfig(t) != encrypted || f.invalidations != 1 {
		t.Fatal("valid A/B/A receipt changed desired secrets or failed to invalidate once")
	}
}

type configAppliedHookRepo struct {
	ports.NodeRepo
	beforeConfirm func(context.Context, int64, int64, domain.NodeConfigIntent) error
	calls         int
}

func (r *configAppliedHookRepo) ConfirmAppliedConfig(ctx context.Context, nodeID, panelID int64, expected domain.NodeConfigIntent) (bool, error) {
	r.calls++
	if r.beforeConfirm != nil {
		if err := r.beforeConfirm(ctx, nodeID, panelID, expected); err != nil {
			return false, err
		}
	}
	return r.NodeRepo.ConfirmAppliedConfig(ctx, nodeID, panelID, expected)
}

func TestNativeConfigAppliedDoesNotConfirmConcurrentSameStampEdit(t *testing.T) {
	for _, field := range []string{"settings", "enabled"} {
		t.Run(field, func(t *testing.T) {
			f := newConfigAppliedFixture(t)
			response := f.mint(t)
			before := f.storedNode(t)
			var edited *domain.Node
			hook := &configAppliedHookRepo{NodeRepo: f.repos.Node}
			hook.beforeConfirm = func(ctx context.Context, nodeID, panelID int64, expected domain.NodeConfigIntent) error {
				if nodeID != before.ID || panelID != before.PanelID || expected != before.ConfigIntent() {
					t.Fatal("service did not bind confirmation to the validated current snapshot")
				}
				current, err := f.repos.Node.GetByID(ctx, nodeID)
				if err != nil {
					return err
				}
				if field == "settings" {
					current.InboundSettings = `{"decryption":"none","fixture_secret":"racing-edit"}`
					err = f.repos.Node.UpdateInboundConfig(ctx, current)
				} else {
					err = f.repos.Node.UpdateEnabled(ctx, nodeID, false)
				}
				if err != nil {
					return err
				}
				edited = f.storedNode(t)
				if !edited.ConfigSyncedAt.Equal(*before.ConfigSyncedAt) || edited.ConfigIntent() == before.ConfigIntent() {
					t.Fatal("race fixture must change intent without changing its capture timestamp")
				}
				return nil
			}
			f.service.nodes = hook
			if _, err := f.service.Sync(t.Context(), f.receipt(response)); err != nil {
				t.Fatal(err)
			}
			if hook.calls != 1 || edited == nil {
				t.Fatal("valid full receipt did not reach the post-validation confirmation hook")
			}
			assertConfigAckOnly(t, edited, f.storedNode(t), false)
			if f.invalidations != 0 {
				t.Fatal("racing stale ACK invalidated the newly edited configuration")
			}
		})
	}
}

func TestNativeConfigAppliedPropagatesConfirmationStorageError(t *testing.T) {
	f := newConfigAppliedFixture(t)
	response := f.mint(t)
	before := f.storedNode(t)
	failure := errors.New("fixture confirmation storage failure")
	f.service.nodes = &configAppliedHookRepo{
		NodeRepo:      f.repos.Node,
		beforeConfirm: func(context.Context, int64, int64, domain.NodeConfigIntent) error { return failure },
	}
	if _, err := f.service.Sync(t.Context(), f.receipt(response)); !errors.Is(err, failure) {
		t.Fatalf("confirmation storage error = %v, want wrapped fixture failure", err)
	}
	assertConfigAckOnly(t, before, f.storedNode(t), false)
	if f.invalidations != 0 {
		t.Fatal("failed storage confirmation invalidated render cache")
	}
	// The durable receipt can replay once storage is available; no new config,
	// credential, or agent epoch needs to be manufactured to confirm it.
	f.service.nodes = f.repos.Node
	if _, err := f.service.Sync(t.Context(), f.receipt(response)); err != nil {
		t.Fatal(err)
	}
	assertConfigAckOnly(t, before, f.storedNode(t), true)
	if f.invalidations != 1 {
		t.Fatal("successful storage retry must invalidate exactly once")
	}
}

func TestNativeConfigAppliedRequiresListenerInMintedDocument(t *testing.T) {
	f := newConfigAppliedFixture(t)
	response := f.mint(t)
	// Mint a valid historical empty closure, then leave the current managed
	// node in the database. Merely naming it Applied in a full report must not
	// confirm a listener that the acknowledged document never contained.
	emptyConfig := *response.Config.Body
	emptyConfig.Listeners, emptyConfig.Coverage = []nodeprotocol.Listener{}, nodeprotocol.SegmentCounts{}
	canonical, err := json.Marshal(emptyConfig)
	if err != nil {
		t.Fatal(err)
	}
	stream, _, err := f.repos.NodeAgent.MintStream(t.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig, canonical, f.now)
	if err != nil {
		t.Fatal(err)
	}
	response.Config.Version = nodeprotocol.Version{Epoch: f.agent.Epoch, Version: stream.DesiredVersion}
	response.Config.ETag = nodeprotocol.ETag(stream.DesiredETag)
	before := f.storedNode(t)
	if _, err := f.service.Sync(t.Context(), f.receipt(response)); err != nil {
		t.Fatal(err)
	}
	assertConfigAckOnly(t, before, f.storedNode(t), false)
	if f.invalidations != 0 {
		t.Fatal("unminted listener receipt invalidated render cache")
	}
}
