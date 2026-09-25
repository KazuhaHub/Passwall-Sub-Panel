package nodesync_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
)

// refusedSyncService fails the test if it is ever reached. A refused report must
// be turned away at the HTTP boundary; reaching the coordinator would mean the
// gate moved.
type refusedSyncService struct{ t *testing.T }

func (s refusedSyncService) Sync(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
	s.t.Fatal("a refused report reached the coordinator")
	return nodeprotocol.SyncResponse{}, nil
}

// TestNodeSyncRecordsARefusedReport drives PSP's real HTTP boundary and real
// repository with a report whose wire generation this panel has not reviewed.
//
// WHY THE REPORT IS HAND-BUILT RATHER THAN PRODUCED BY THE REAL NODE, which is
// how contract_live_test.go works and would otherwise be the right instinct here:
// a conforming Passwall-Node CANNOT produce a report this panel refuses.
// internal/agent/sync.go calls protocol.ValidateNodeReport before sending, and
// that function is ValidateNodeReportBase plus the host subtree — a superset of
// what PSP applies on receipt. Both ends compile the same validator from the same
// module, so the node rejects its own report first and nothing is ever sent.
//
// That is a safety property, not a gap, and it says what this path is actually
// for: a peer built against a DIFFERENT generation of the shared module (the
// reverse-skew case this exists to make visible), a third-party implementation of
// the contract, or a panel that has narrowed its own declared range below what
// its fleet reports. Driving it with the real node would require a seam in the
// node that exists only to defeat its own validator, which would be a worse
// artifact than this test.
func TestNodeSyncRecordsARefusedReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "psp.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)

	credential := "pspn_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	digest := sha256.Sum256([]byte(credential))
	agentRow := &domain.NodeAgent{
		AgentID: "agt_refusal", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27",
	}
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "refusal", URL: "psp://" + agentRow.AgentID}
	if err := repos.NativeAgentProvisioning.Create(ctx, panel, agentRow); err != nil {
		t.Fatal(err)
	}

	// A GOOD OBSERVATION FIRST. This is the state that made the defect invisible:
	// the row remembers a generation that WAS accepted, and every later refusal
	// leaves it untouched, so the panel went on reporting the node as healthy.
	accepted := time.Now().UTC().Add(-time.Hour)
	if err := repos.NodeAgent.UpdateProtocolObservation(ctx, agentRow.AgentID,
		nodeprotocol.ProtocolVersion1, nodeprotocol.AgentUpgradeCapabilities(), accepted); err != nil {
		t.Fatal(err)
	}
	before, err := repos.NodeAgent.GetByAgentID(ctx, agentRow.AgentID)
	if err != nil {
		t.Fatal(err)
	}

	syncHandler, err := handler.NewNodeSyncHandler(
		refusedSyncService{t: t},
		handler.NodeAuthenticatorFunc(func(*http.Request) (string, error) { return agentRow.AgentID, nil }),
		handler.WithNodeRefusalRecorder(repos.NodeAgent),
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(syncHandler)
	defer server.Close()

	ahead := nodeprotocol.ProtocolVersion1 + 1
	report := nodeprotocol.NodeReport{
		AgentID: agentRow.AgentID, ProtocolVersion: ahead,
		ReportedAtMS: time.Now().UnixMilli(), AgentVersion: "ahead-of-panel",
		Have: map[string]nodeprotocol.StreamState{
			nodeprotocol.StreamConfig:     {},
			nodeprotocol.StreamRoster:     {},
			nodeprotocol.StreamDirectives: {},
		},
		Capabilities: nodeprotocol.AgentUpgradeCapabilities(),
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	post := func() int {
		t.Helper()
		response, err := http.Post(server.URL+"/v1/node/sync", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}

	if status := post(); status != http.StatusBadRequest {
		t.Fatalf("refused report answered %d, want %d", status, http.StatusBadRequest)
	}
	firstRound, err := repos.NodeAgent.GetByAgentID(ctx, agentRow.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRound.RefusedFirstAt == nil {
		t.Fatal("the first refusal recorded no start time")
	}

	// A SECOND ROUND MUST NOT MOVE "SINCE WHEN". A node retrying every thirty
	// seconds would otherwise always look like it had just started failing, and
	// the one number an operator needs — how long this has been going on — would
	// be the one number that is always wrong.
	time.Sleep(20 * time.Millisecond)
	if status := post(); status != http.StatusBadRequest {
		t.Fatalf("second refused report answered %d, want %d", status, http.StatusBadRequest)
	}

	after, err := repos.NodeAgent.GetByAgentID(ctx, agentRow.AgentID)
	if err != nil {
		t.Fatal(err)
	}

	if !after.CurrentlyRefused() {
		t.Fatal("the refusal was not recorded")
	}
	if after.RefusedProtocolVersion == nil || *after.RefusedProtocolVersion != ahead {
		t.Fatalf("refused generation = %v, want %d", after.RefusedProtocolVersion, ahead)
	}
	if after.RefusedReason != domain.NodeRefusalProtocolGeneration {
		t.Fatalf("refusal reason = %q, want %q", after.RefusedReason, domain.NodeRefusalProtocolGeneration)
	}

	// THE OBSERVATION SURVIVES INTACT. compat-policy 3.2 requires that losing
	// contact must not erase the last valid observation; this is that rule under
	// test rather than in prose.
	if after.ObservedProtocolVersion != before.ObservedProtocolVersion {
		t.Fatalf("observed generation moved: %d -> %d", before.ObservedProtocolVersion, after.ObservedProtocolVersion)
	}
	if after.ProtocolObservedAt == nil || !after.ProtocolObservedAt.Equal(*before.ProtocolObservedAt) {
		t.Fatalf("observation timestamp moved: %v -> %v", before.ProtocolObservedAt, after.ProtocolObservedAt)
	}
	if len(after.ObservedCapabilities) != len(before.ObservedCapabilities) {
		t.Fatalf("observed capabilities changed: %v -> %v", before.ObservedCapabilities, after.ObservedCapabilities)
	}

	// last_seen IS NOT TOUCHED. nodehealth reads it to decide whether an agent is
	// offline; advancing it here would flip a refused node to "online" and then
	// measure it against thresholds using frozen host metrics.
	if after.LastSeen != before.LastSeen {
		t.Fatalf("last_seen advanced on a refused report: %v -> %v", before.LastSeen, after.LastSeen)
	}

	// "Since when" is the FIRST refusal of the run, not the latest.
	if after.RefusedFirstAt == nil || after.RefusedAt == nil {
		t.Fatal("a refusal must carry both its first and its latest time")
	}
	if !after.RefusedFirstAt.Equal(*firstRound.RefusedFirstAt) {
		t.Fatalf("a retry moved the start of the refusal run: %v -> %v",
			firstRound.RefusedFirstAt, after.RefusedFirstAt)
	}
	if !after.RefusedAt.After(*after.RefusedFirstAt) {
		t.Fatalf("the latest refusal %v did not advance past the first %v", after.RefusedAt, after.RefusedFirstAt)
	}
	if _, offset := after.RefusedFirstAt.Zone(); offset != 0 {
		t.Fatalf("refusal times must be stored in UTC, got offset %d", offset)
	}

	// AND IT CLEARS. A refusal is a current fact, not a sticky mark: the next
	// accepted report ends the run, or a recovered node would carry it forever.
	recovered := time.Now().UTC()
	if err := repos.NodeAgent.UpdateProtocolObservation(ctx, agentRow.AgentID,
		nodeprotocol.ProtocolVersion1, nodeprotocol.AgentUpgradeCapabilities(), recovered); err != nil {
		t.Fatal(err)
	}
	cleared, err := repos.NodeAgent.GetByAgentID(ctx, agentRow.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.CurrentlyRefused() || cleared.RefusedAt != nil || cleared.RefusedReason != "" {
		t.Fatalf("an accepted report did not end the refusal run: %+v", cleared)
	}
}
