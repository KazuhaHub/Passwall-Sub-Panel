package nodesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/corefixtures"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// attachmentWriteCounter is the real client repository, counting the
// convergence writes a sync makes.
type attachmentWriteCounter struct {
	ports.PSPClientRepo
	writes []domain.PSPClientInbound
}

func (r *attachmentWriteCounter) UpdateInboundState(ctx context.Context, inbound domain.PSPClientInbound) error {
	r.writes = append(r.writes, inbound)
	return r.PSPClientRepo.UpdateInboundState(ctx, inbound)
}

// A full report replays the state of every roster object, and almost always
// the state it reported last time. The attachment row is written when what the
// node reports moves it, and not once per client per attachment per full
// report of every server for a row that already says so.
func TestSyncWritesAttachmentStateOnlyWhenItMoves(t *testing.T) {
	ctx := t.Context()
	repos := sqlstore.NewRepos(openMigratedTestDB(t))
	user := &domain.User{
		UPN: "steady@example.test", Email: "steady@example.test",
		SSOProvider: domain.SSOProviderLocal, SSOSubject: "steady@example.test",
		Role: domain.RoleUser, SubToken: "steady-sub-token", UUID: "22222222-2222-4222-8222-222222222222",
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repos.User.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	node := &domain.Node{
		PanelID: 9, InboundID: 1, DisplayName: "steady", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 443, InboundListen: "0.0.0.0",
		InboundRemark: "steady", InboundSettings: `{}`, StreamSettings: `{}`,
		Sniffing: `{}`, Allocate: `{}`, Region: "CA", Enabled: true,
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	client := &domain.PSPClient{
		UserID: user.ID, PanelID: 9, Email: "steady@psp.local",
		UUID: user.UUID, Password: "secret", DesiredEnable: true, DesiredMinted: true,
	}
	clientID, err := repos.PSPClient.Create(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	client.ID = clientID
	if err := repos.PSPClient.SetInbounds(ctx, client.ID, []domain.PSPClientInbound{{
		ClientID: client.ID, NodeID: node.ID, State: domain.ClientApplyPending,
	}}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("steady credential"))
	agent := &domain.NodeAgent{
		AgentID: "agt_steady", PanelID: 9, Epoch: 1,
		CredentialSHA256:  hex.EncodeToString(digest[:]),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27",
	}
	if err := repos.NodeAgent.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	counter := &attachmentWriteCounter{PSPClientRepo: repos.PSPClient}
	invalidations := 0
	service, err := New(Options{CoreCatalog: corefixtures.Static{},
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Tasks: repos.NodeAgentTask, Users: repos.User,
		Clients: counter, Nodes: repos.Node, Settings: repos.ScopedSettings,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRenderInvalidator(func() { invalidations++ })

	first, err := service.Sync(ctx, nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.UnixMilli(), Have: emptyProtocolHave(),
	})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]nodeprotocol.StreamState{
		nodeprotocol.StreamConfig:     {Applied: first.Config.Version, ETag: first.Config.ETag},
		nodeprotocol.StreamRoster:     {Applied: first.Roster.Version, ETag: first.Roster.ETag},
		nodeprotocol.StreamDirectives: {Applied: first.Directives.Version, ETag: first.Directives.ETag},
	}
	// report is a full report in which the roster object is in state; each one
	// acknowledges the directives the previous answer carried, as an agent does.
	report := func(state nodeprotocol.ObjectState, firstFailedAtMS int64) {
		t.Helper()
		now = now.Add(30 * time.Second)
		response, err := service.Sync(ctx, nodeprotocol.NodeReport{
			AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
			ReportedAtMS: now.UnixMilli(), Have: have,
			Objects: []nodeprotocol.ObjectStatus{
				{Stream: nodeprotocol.StreamConfig, Key: string(nodeprotocol.NewListenerKey(node.ID)), State: nodeprotocol.ObjectApplied, SinceVersion: first.Config.Version},
				{Stream: nodeprotocol.StreamRoster, Key: string(nodeprotocol.NewClientKey(client.ID)), State: state, SinceVersion: first.Roster.Version, FirstFailedAtMS: firstFailedAtMS},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !response.Roster.Unchanged {
			t.Fatal("the roster moved: the fixture is not a steady state")
		}
		have[nodeprotocol.StreamDirectives] = nodeprotocol.StreamState{Applied: response.Directives.Version, ETag: response.Directives.ETag}
	}
	stored := func() domain.PSPClientInbound {
		t.Helper()
		attachments, err := repos.PSPClient.ListInbounds(ctx, client.ID)
		if err != nil || len(attachments) != 1 {
			t.Fatalf("attachments = (%+v, %v)", attachments, err)
		}
		return attachments[0]
	}
	expectWrites := func(step string, want int) {
		t.Helper()
		if len(counter.writes) != want {
			t.Fatalf("%s: attachment writes = %d, want %d: %+v", step, len(counter.writes), want, counter.writes)
		}
		counter.writes = nil
	}
	expectWrites("a report that has not applied the roster", 0)

	report(nodeprotocol.ObjectApplied, 0)
	expectWrites("first applied receipt", 1)
	applied := stored()
	if !applied.Applied() || applied.AppliedVersion != first.Roster.Version.Version ||
		applied.AppliedEmail != client.Email || applied.AppliedUUID != client.UUID || applied.AppliedPassword != client.Password ||
		applied.FirstFailedAt != nil {
		t.Fatalf("applied attachment = %+v", applied)
	}
	invalidated := invalidations

	report(nodeprotocol.ObjectApplied, 0)
	report(nodeprotocol.ObjectApplied, 0)
	expectWrites("replayed applied receipts", 0)
	if got := stored(); !reflect.DeepEqual(got, applied) {
		t.Fatalf("a replayed receipt moved the attachment: %+v, was %+v", got, applied)
	}
	if invalidations != invalidated {
		t.Fatalf("a replayed receipt invalidated subscriptions %d times", invalidations-invalidated)
	}

	// A row that moved under the sync is written again: the comparison is with
	// the row as this sync loaded it, not with what the last sync wrote.
	if err := repos.PSPClient.UpdateInboundState(ctx, domain.PSPClientInbound{
		ClientID: client.ID, NodeID: node.ID, State: domain.ClientApplyPending,
	}); err != nil {
		t.Fatal(err)
	}
	report(nodeprotocol.ObjectApplied, 0)
	expectWrites("applied receipt over a row reset to pending", 1)
	if got := stored(); !reflect.DeepEqual(got, applied) {
		t.Fatalf("attachment after the reset = %+v, want %+v", got, applied)
	}

	failedAt := now.Add(-time.Minute)
	report(nodeprotocol.ObjectPending, failedAt.UnixMilli())
	expectWrites("first pending report", 1)
	pending := stored()
	if pending.State != domain.ClientApplyPending || pending.FirstFailedAt == nil || !pending.FirstFailedAt.Equal(failedAt) {
		t.Fatalf("pending attachment = %+v", pending)
	}
	report(nodeprotocol.ObjectPending, failedAt.UnixMilli())
	expectWrites("replayed pending report", 0)
	// A report that names a different first failure moves the clock.
	report(nodeprotocol.ObjectPending, failedAt.Add(time.Second).UnixMilli())
	expectWrites("pending report with a later first failure", 1)

	report(nodeprotocol.ObjectApplied, 0)
	expectWrites("applied after pending", 1)
	if got := stored(); !reflect.DeepEqual(got, applied) {
		t.Fatalf("attachment after recovery = %+v, want %+v", got, applied)
	}
}

// attachmentSettled restates, without reading the row, what
// UpdateInboundState does with it. It may only say "settled" for a write the
// repository would turn into the row it already has. This runs the repository
// over every pairing of stored row and write and holds the answer to that.
func TestAttachmentSettledNeverSkipsAWriteThatChangesTheRow(t *testing.T) {
	ctx := t.Context()
	db := openMigratedTestDB(t)
	repos := sqlstore.NewRepos(db)
	earlier := time.Date(2026, time.November, 27, 11, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Minute)
	states := []domain.ClientApplyState{
		domain.ClientApplyPending, domain.ClientApplyApplied, domain.ClientApplyRejected, domain.ClientApplyBlocked,
	}
	type credentials struct{ email, uuid, password string }
	var rows, writes []domain.PSPClientInbound
	for _, state := range states {
		for _, failedAt := range []*time.Time{nil, &earlier, &later} {
			for _, version := range []uint64{0, 4, 5} {
				for _, cred := range []credentials{{}, {"a@psp.local", "uuid-a", "pw-a"}, {"a@psp.local", "uuid-b", "pw-a"}} {
					item := domain.PSPClientInbound{
						State: state, FirstFailedAt: failedAt, AppliedVersion: version,
						AppliedEmail: cred.email, AppliedUUID: cred.uuid, AppliedPassword: cred.password,
					}
					rows, writes = append(rows, item), append(writes, item)
				}
			}
		}
	}
	const nodeID = int64(77)
	settled, moved := 0, 0
	for i, row := range rows {
		clientID, err := repos.PSPClient.Create(ctx, &domain.PSPClient{UserID: 1, PanelID: 9, Email: fmt.Sprintf("c%d@psp.local", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := repos.PSPClient.SetInbounds(ctx, clientID, []domain.PSPClientInbound{{ClientID: clientID, NodeID: nodeID}}); err != nil {
			t.Fatal(err)
		}
		row.ClientID, row.NodeID = clientID, nodeID
		for _, write := range writes {
			write.ClientID, write.NodeID = clientID, nodeID
			if err := db.Table("psp_client_inbounds").Where("client_id = ? AND node_id = ?", clientID, nodeID).Updates(map[string]any{
				"state": string(row.State), "first_failed_at": row.FirstFailedAt, "applied_version": row.AppliedVersion,
				"applied_email": row.AppliedEmail, "applied_uuid": row.AppliedUUID, "applied_password": row.AppliedPassword,
			}).Error; err != nil {
				t.Fatal(err)
			}
			before, err := repos.PSPClient.ListInbounds(ctx, clientID)
			if err != nil || len(before) != 1 {
				t.Fatalf("stored attachment = (%+v, %v)", before, err)
			}
			if !attachmentSettled(before[0], write) {
				moved++
				continue
			}
			settled++
			if err := repos.PSPClient.UpdateInboundState(ctx, write); err != nil {
				t.Fatal(err)
			}
			after, err := repos.PSPClient.ListInbounds(ctx, clientID)
			if err != nil || len(after) != 1 {
				t.Fatalf("written attachment = (%+v, %v)", after, err)
			}
			if !reflect.DeepEqual(after[0], before[0]) {
				t.Fatalf("called settled, but the write of %+v turned %+v into %+v", write, before[0], after[0])
			}
		}
	}
	// Both answers are given: a function that never said "settled" would pass
	// the loop above while saving nothing.
	if settled == 0 || moved == 0 {
		t.Fatalf("settled = %d, moved = %d: the matrix must exercise both", settled, moved)
	}
	for _, tc := range []struct {
		name          string
		stored, write domain.PSPClientInbound
		want          bool
	}{
		{"applied replay", domain.PSPClientInbound{State: domain.ClientApplyApplied, AppliedVersion: 5, AppliedUUID: "u"},
			domain.PSPClientInbound{State: domain.ClientApplyApplied, AppliedVersion: 5, AppliedUUID: "u"}, true},
		{"applied at a newer roster version", domain.PSPClientInbound{State: domain.ClientApplyApplied, AppliedVersion: 4, AppliedUUID: "u"},
			domain.PSPClientInbound{State: domain.ClientApplyApplied, AppliedVersion: 5, AppliedUUID: "u"}, false},
		{"applied with another credential", domain.PSPClientInbound{State: domain.ClientApplyApplied, AppliedVersion: 5, AppliedUUID: "u"},
			domain.PSPClientInbound{State: domain.ClientApplyApplied, AppliedVersion: 5, AppliedUUID: "v"}, false},
		{"pending replay keeps its clock", domain.PSPClientInbound{State: domain.ClientApplyPending, FirstFailedAt: &earlier},
			domain.PSPClientInbound{State: domain.ClientApplyPending}, true},
		{"pending with no clock starts one", domain.PSPClientInbound{State: domain.ClientApplyPending},
			domain.PSPClientInbound{State: domain.ClientApplyPending}, false},
		{"blocked replay", domain.PSPClientInbound{State: domain.ClientApplyBlocked},
			domain.PSPClientInbound{State: domain.ClientApplyBlocked}, true},
		{"state change", domain.PSPClientInbound{State: domain.ClientApplyPending, FirstFailedAt: &earlier},
			domain.PSPClientInbound{State: domain.ClientApplyRejected, FirstFailedAt: &earlier}, false},
	} {
		if got := attachmentSettled(tc.stored, tc.write); got != tc.want {
			t.Errorf("%s: settled = %v, want %v", tc.name, got, tc.want)
		}
	}
}
