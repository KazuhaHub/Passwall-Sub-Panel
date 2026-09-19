package nodediagnostics

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fixture struct {
	service *Service
	repos   ports.Repos
	db      *gorm.DB
	panelID int64
	now     time.Time
}

// newFixture provisions one native server, optionally advertising the
// capability.
func newFixture(t *testing.T, advertise bool) *fixture {
	t.Helper()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "diagnostics.db"))
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := db.DB()
	t.Cleanup(func() { _ = connection.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	panel := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "diagnostics fixture", URL: "psp://agt_diag"}
	agent := &domain.NodeAgent{AgentID: "agt_diag", CredentialSHA256: strings.Repeat("b", 64)}
	if advertise {
		observedAt := time.Now().UTC().Truncate(time.Millisecond)
		agent.ObservedProtocolVersion = nodeprotocol.ProtocolVersion1
		agent.ObservedCapabilities = []string{nodeprotocol.TaskCapability(Kind)}
		agent.ProtocolObservedAt = &observedAt
	}
	if err := repos.NativeAgentProvisioning.Create(context.Background(), panel, agent); err != nil {
		t.Fatal(err)
	}
	ids, err := idgen.NewTaskIDMinter()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{repos: repos, db: db, panelID: panel.ID, now: time.Now().UTC().Truncate(time.Millisecond)}
	f.service, err = New(Options{
		Panels: repos.XUIPanel, Agents: repos.NodeAgent, Tasks: repos.NodeAgentTask,
		Settings: repos.Settings, IDs: ids, Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var fullRequest = Request{
	SchemaVersion: nodeprotocol.DiagnosticsSchemaVersion,
	Sections:      []string{nodeprotocol.DiagnosticsSectionHost, nodeprotocol.DiagnosticsSectionState},
	MaxEvents:     50,
}

// THE CAPABILITY IS THE GATE, and absence means the node cannot do it — which is
// a different statement from a node that failed, and has to be told apart before
// a task exists at all.
func TestCreateRequiresTheNodeToAdvertiseTheCapability(t *testing.T) {
	f := newFixture(t, false)
	_, _, err := f.service.Create(context.Background(), f.panelID, fullRequest)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("an unadvertised node was accepted: %v", err)
	}
}

func TestCreateRejectsARequestOutsideSection131(t *testing.T) {
	f := newFixture(t, true)
	for name, request := range map[string]Request{
		"an unknown section": {SchemaVersion: nodeprotocol.DiagnosticsSchemaVersion, Sections: []string{"logfiles"}},
		"a bad schema":       {SchemaVersion: 9},
		"too many events":    {SchemaVersion: nodeprotocol.DiagnosticsSchemaVersion, MaxEvents: nodeprotocol.MaxDiagnosticsEvents + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := f.service.Create(context.Background(), f.panelID, request); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("error = %v, want ErrValidation", err)
			}
		})
	}
}

// SECTION 13.1 BOUNDS THE DEADLINE AT FIVE MINUTES, which is what keeps a
// read-only collection from staying authorized long after anyone is waiting.
func TestCreateBoundsTheDeadlineToTheProtocolWindow(t *testing.T) {
	f := newFixture(t, true)
	status, created, err := f.service.Create(context.Background(), f.panelID, fullRequest)
	if err != nil || !created {
		t.Fatalf("create = (%+v, %v, %v)", status, created, err)
	}
	window := status.NotAfterMS - f.now.UnixMilli()
	if window <= 0 || window > nodeprotocol.MaxDiagnosticsNotAfter.Milliseconds() {
		t.Fatalf("authorization window = %d ms, want within (0, %d]",
			window, nodeprotocol.MaxDiagnosticsNotAfter.Milliseconds())
	}
	if status.Status != domain.NodeAgentTaskQueued {
		t.Fatalf("a new task is %q, want queued", status.Status)
	}
}

// ONE ACTIVE DIAGNOSTIC PER AGENT. A second call is answered with the collection
// already running, and the answer describes the TASK rather than the request —
// which is why the sections come back from the stored arguments.
func TestASecondRequestMergesIntoTheRunningCollection(t *testing.T) {
	f := newFixture(t, true)
	first, created, err := f.service.Create(context.Background(), f.panelID, fullRequest)
	if err != nil || !created {
		t.Fatalf("first create = (%+v, %v, %v)", first, created, err)
	}
	// Deliberately different arguments: the merge is per agent, not per request.
	second, created, err := f.service.Create(context.Background(), f.panelID, Request{
		SchemaVersion: nodeprotocol.DiagnosticsSchemaVersion,
		Sections:      []string{nodeprotocol.DiagnosticsSectionEvents},
		MaxEvents:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("a second diagnostic was minted while one was active")
	}
	if second.TaskID != first.TaskID {
		t.Fatalf("merged to %s, want the running %s", second.TaskID, first.TaskID)
	}
	if len(second.Sections) != 2 || second.Sections[0] != nodeprotocol.DiagnosticsSectionHost {
		t.Fatalf("status reported %v, want the sections actually being collected", second.Sections)
	}
}

// A FINISHED TASK IS NOT ACTIVE, so the same operator can collect again
// tomorrow. Without that the "one active" rule would quietly become "one ever".
func TestAFutureRequestMintsAgainAfterTheFirstFinishes(t *testing.T) {
	f := newFixture(t, true)
	first, _, err := f.service.Create(context.Background(), f.panelID, fullRequest)
	if err != nil {
		t.Fatal(err)
	}
	// The node answers; the status is what makes it non-terminal. Driven
	// directly rather than through a whole collection, because what is under
	// test here is the query's notion of "active".
	if err := f.db.Exec(`UPDATE node_agent_tasks SET status = ?, completed_at = ? WHERE task_id = ?`,
		string(domain.NodeAgentTaskSucceeded), f.now, first.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	second, created, err := f.service.Create(context.Background(), f.panelID, fullRequest)
	if err != nil || !created {
		t.Fatalf("a second collection after the first finished = (%+v, %v, %v)", second, created, err)
	}
	if second.TaskID == first.TaskID {
		t.Fatal("the finished task was reused instead of collecting again")
	}
}

// Get refuses a task belonging to another agent, so a task id is not a way to
// read a machine the caller did not name.
func TestGetRejectsATaskThatIsNotThisAgents(t *testing.T) {
	f := newFixture(t, true)
	if _, err := f.service.Get(context.Background(), f.panelID, "no-such-task"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
