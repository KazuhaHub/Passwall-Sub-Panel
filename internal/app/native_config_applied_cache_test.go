package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
)

func syncNativeCacheFixture(t *testing.T, a *App, credential string, report nodeprotocol.NodeReport) nodeprotocol.SyncResponse {
	t.Helper()
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("native cache fixture sync HTTP status = %d, want 200", recorder.Code)
	}
	var response nodeprotocol.SyncResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeNativeCacheURI(t *testing.T, output *render.Output) string {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(string(output.Body))
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded)
}

func TestBuildNativeConfigAppliedInvalidatesBothSubscriptionCacheLayers(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	// Build, rather than a separately assembled service, owns both invalidator
	// bindings. Serve the real authenticated sync handler in memory; Run is not
	// called, so there are no listeners, worker ticks, or external requests.
	credential := strings.Repeat("fixture-native-cache-credential-", 2)
	digest := sha256.Sum256([]byte(credential))
	agent := &domain.NodeAgent{AgentID: "agt_cache_wiring", Epoch: 7, CredentialSHA256: hex.EncodeToString(digest[:])}
	panel := &domain.XUIPanel{Name: "cache-native", Kind: domain.PanelKindPSP, URL: "psp://" + agent.AgentID}
	if err := a.repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	if err := a.xuiPool.Add(panel); err != nil {
		t.Fatal(err)
	}
	captured, pending := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(-30*time.Minute)
	node := &domain.Node{
		PanelID: panel.ID, InboundID: 1, DisplayName: "cache-original-node", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 444, ObservedProtocol: "vless", ObservedPort: 444,
		InboundListen: "0.0.0.0", InboundRemark: "cache-listener", InboundSettings: `{"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{}`, Allocate: `{}`, Enabled: true,
		ConfigSyncedAt: &captured, ConfigSyncState: domain.ConfigSyncPending, ConfigPendingSince: &pending,
	}
	if err := a.repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	group := &domain.Group{Slug: "cache-group", Name: "cache-group", TagFilter: domain.TagFilter{All: true}}
	if err := a.repos.Group.Create(ctx, group); err != nil {
		t.Fatal(err)
	}
	user := &domain.User{
		UPN: "cache@example.test", Email: "cache@example.test", SSOProvider: domain.SSOProviderLocal,
		SSOSubject: "cache@example.test", Role: domain.RoleUser, GroupID: group.ID, Enabled: true,
		UUID: "33333333-3333-4333-8333-333333333333", SubToken: "fixture-cache-subscription-token",
	}
	if err := a.repos.User.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	client := &domain.PSPClient{
		UserID: user.ID, PanelID: panel.ID, Email: "u-cache@psp.local",
		UUID: user.UUID, Password: "fixture-password",
	}
	client.SetDesiredLifecycle(domain.UserLifecycle{Enable: true})
	client.ID, err = a.repos.PSPClient.Create(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.repos.PSPClient.SetInbounds(ctx, client.ID, []domain.PSPClientInbound{{
		ClientID: client.ID, NodeID: node.ID, State: domain.ClientApplyPending,
	}}); err != nil {
		t.Fatal(err)
	}
	have := map[string]nodeprotocol.StreamState{
		nodeprotocol.StreamConfig: {}, nodeprotocol.StreamRoster: {}, nodeprotocol.StreamDirectives: {},
	}
	initial := syncNativeCacheFixture(t, a, credential, nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, Have: have,
	})
	if initial.Config.Body == nil || len(initial.Config.Body.Listeners) != 1 ||
		initial.Roster.Body == nil || len(initial.Roster.Body.Clients) != 1 {
		t.Fatal("native fixture did not mint its pending listener and roster identity")
	}
	// URI-list needs no on-disk template. This warms both actual cache layers:
	// group.NodesFor retains a pending Node; render retains the empty output.
	// No listener counters exist yet, so the live facade cannot hide a stale
	// pending group snapshot by supplying an alternate runtime inbound.
	warm, err := a.render.RenderForUserCached(ctx, user, domain.ClientURIList)
	if err != nil {
		t.Fatal(err)
	}
	if decodeNativeCacheURI(t, warm) != "" {
		t.Fatal("pending fixture must not render a locally confirmed inbound")
	}
	warmAgain, err := a.render.RenderForUserCached(ctx, user, domain.ClientURIList)
	if err != nil || warmAgain != warm {
		t.Fatal("fixture did not warm the actual final-output cache")
	}
	have[nodeprotocol.StreamConfig] = nodeprotocol.StreamState{Applied: initial.Config.Version, ETag: initial.Config.ETag}
	have[nodeprotocol.StreamRoster] = nodeprotocol.StreamState{Applied: initial.Roster.Version, ETag: initial.Roster.ETag}
	ack := nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, Have: have,
		Objects: []nodeprotocol.ObjectStatus{
			{
				Stream: nodeprotocol.StreamConfig, Key: string(nodeprotocol.NewListenerKey(node.ID)),
				State: nodeprotocol.ObjectApplied, SinceVersion: initial.Config.Version,
			},
			{
				Stream: nodeprotocol.StreamRoster, Key: string(nodeprotocol.NewClientKey(client.ID)),
				State: nodeprotocol.ObjectApplied, SinceVersion: initial.Roster.Version,
			},
		},
	}
	syncNativeCacheFixture(t, a, credential, ack)
	confirmed, err := a.repos.Node.GetByID(ctx, node.ID)
	if err != nil || confirmed.ConfigSyncState != domain.ConfigSyncSynced || confirmed.ConfigPendingSince != nil {
		t.Fatal("valid full ACK did not commit the pending-node confirmation")
	}
	if !confirmed.ConfigSyncedAt.Equal(captured) {
		t.Fatal("ACK must preserve the original capture timestamp")
	}
	fresh, err := a.render.RenderForUserCached(ctx, user, domain.ClientURIList)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == warm {
		t.Fatal("native ACK left the final-output cache populated with pending output")
	}
	uri, err := url.Parse(decodeNativeCacheURI(t, fresh))
	if err != nil || uri.Scheme != "vless" || uri.Host != "node.example.test:444" || uri.Fragment != node.DisplayName {
		t.Fatal("native ACK did not immediately refresh the cached group Node to use its confirmed local config")
	}
	// Metadata is deliberately written directly through the repository here,
	// without a normal mutation's invalidation. It acts as a cache-read probe:
	// an idempotent ACK must leave BOTH warmed caches intact, not reload this
	// new name. No listener-intent field or captured timestamp changes.
	metadata := *confirmed
	metadata.DisplayName = "cache-probe-new-name"
	if err := a.repos.Node.UpdateMetadata(ctx, &metadata); err != nil {
		t.Fatal(err)
	}
	syncNativeCacheFixture(t, a, credential, ack)
	replayed, err := a.render.RenderForUserCached(ctx, user, domain.ClientURIList)
	if err != nil || replayed != fresh {
		t.Fatal("duplicate full ACK discarded the warmed final-output cache")
	}
	uncachedOutput, err := a.render.RenderForUser(ctx, user, domain.ClientURIList)
	if err != nil {
		t.Fatal(err)
	}
	uncachedURI, err := url.Parse(decodeNativeCacheURI(t, uncachedOutput))
	if err != nil || uncachedURI.Fragment != node.DisplayName {
		t.Fatal("duplicate full ACK discarded the warmed enabled-node cache")
	}
}
