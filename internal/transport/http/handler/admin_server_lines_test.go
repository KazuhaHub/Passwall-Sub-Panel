package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func serverLinesFleet() []*domain.Node {
	return []*domain.Node{
		{ID: 1, PanelID: 7, DisplayName: "JP", Relays: []domain.RelayLine{
			{ID: "fwd", Name: "GZ forward", Address: "gz.example", EntryPanelID: 9, Enabled: true},
			{ID: "ext", Name: "external", Address: "e.example", Enabled: true},
		}},
		{ID: 2, PanelID: 7, DisplayName: "US", EgressPanelID: 9},
	}
}

func callServers(h *AdminServersHandler, method, id string, fn func(*gin.Context)) (int, map[string]any) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: id}}
	fn(c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A direct forward has no node on its entry server, so the node check never saw
// it: deleting the server left the line published, pointing at a forward that no
// longer runs. The delete is refused and names the lines.
func TestServerDeleteRefusedWhenForwardLinesEnter(t *testing.T) {
	h := &AdminServersHandler{nodes: staticNodeLister{nodes: serverLinesFleet()}}
	code, out := callServers(h, http.MethodDelete, "9", h.Delete)
	if code != http.StatusConflict || out["code"] != "server_has_lines" {
		t.Fatalf("%d %v", code, out)
	}
	lines := out["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	line := lines[0].(map[string]any)
	if line["node_id"].(float64) != 1 || line["line_id"] != "fwd" || line["landing_name"] != "JP" ||
		line["line_name"] != "GZ forward" || line["line_type"] != "forward" {
		t.Fatalf("line = %v", line)
	}
}

func TestDeleteImpactListsEnteringLines(t *testing.T) {
	h := &AdminServersHandler{nodes: staticNodeLister{nodes: serverLinesFleet()}}
	code, out := callServers(h, http.MethodGet, "9", h.DeleteImpact)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	if got := out["lines_entering"].([]any); len(got) != 1 || got[0].(map[string]any)["line_id"] != "fwd" {
		t.Fatalf("lines_entering = %v", got)
	}
	if got := out["egress_users"].([]any); len(got) != 1 || got[0].(map[string]any)["node"] != "US" {
		t.Fatalf("egress_users = %v", got)
	}
	if out["nodes"] == nil || len(out["nodes"].([]any)) != 0 || len(out["route_users"].([]any)) != 0 {
		t.Fatalf("nodes/route_users must be empty arrays: %v", out)
	}
	// A server nothing enters through.
	_, out = callServers(h, http.MethodGet, "55", h.DeleteImpact)
	if len(out["lines_entering"].([]any)) != 0 {
		t.Fatalf("lines_entering = %v", out["lines_entering"])
	}
}

// pendingDeleteTasks says a node-delete task is still queued for the given nodes.
type pendingDeleteTasks struct {
	ports.SyncTaskRepo
	nodes map[int64]bool
}

func (r pendingDeleteTasks) HasActiveByTargetAny(_ context.Context, types []domain.SyncTaskType, targetType string, id int64) (bool, error) {
	return targetType == "node" && r.nodes[id] && len(types) == 1 && types[0] == domain.SyncTaskNodeDelete, nil
}

func pendingFleet() []*domain.Node {
	return append(serverLinesFleet(), &domain.Node{ID: 3, PanelID: 7, DisplayName: "SG", Relays: []domain.RelayLine{
		{ID: "fwd2", Name: "SH forward", Address: "sh.example", EntryPanelID: 9, Enabled: true}}})
}

// DeleteAndSync only disables a node until its sync task drains, so its lines
// still count as entering the server. They keep blocking the delete, and the
// answer says which landing is on its way out and what to do about it.
func TestServerDeleteRefusalSaysWhichLandingsArePendingDeletion(t *testing.T) {
	h := (&AdminServersHandler{nodes: staticNodeLister{nodes: pendingFleet()}}).WithSyncTasks(pendingDeleteTasks{nodes: map[int64]bool{3: true}})
	code, out := callServers(h, http.MethodDelete, "9", h.Delete)
	if code != http.StatusConflict || out["code"] != "server_has_lines" {
		t.Fatalf("%d %v", code, out)
	}
	got := map[string]bool{}
	for _, l := range out["lines"].([]any) {
		line := l.(map[string]any)
		got[line["line_id"].(string)] = line["landing_pending_delete"].(bool)
	}
	if len(got) != 2 || got["fwd"] || !got["fwd2"] {
		t.Fatalf("landing_pending_delete by line = %v", got)
	}
	msg, _ := out["error"].(string)
	for _, want := range []string{"remove those lines on their landing nodes", "being deleted", "wait", "detach"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error text %q lacks %q", msg, want)
		}
	}
	// Without a pending landing the text does not talk about deletions.
	h = (&AdminServersHandler{nodes: staticNodeLister{nodes: serverLinesFleet()}}).WithSyncTasks(pendingDeleteTasks{})
	_, out = callServers(h, http.MethodDelete, "9", h.Delete)
	if msg, _ := out["error"].(string); strings.Contains(msg, "being deleted") || out["lines"].([]any)[0].(map[string]any)["landing_pending_delete"] != false {
		t.Fatalf("unexpected pending text: %v", out)
	}
}

func TestDeleteImpactMarksLandingsPendingDeletion(t *testing.T) {
	h := (&AdminServersHandler{nodes: staticNodeLister{nodes: pendingFleet()}}).WithSyncTasks(pendingDeleteTasks{nodes: map[int64]bool{3: true}})
	_, out := callServers(h, http.MethodGet, "9", h.DeleteImpact)
	got := map[string]bool{}
	for _, l := range out["lines_entering"].([]any) {
		line := l.(map[string]any)
		got[line["line_id"].(string)] = line["landing_pending_delete"].(bool)
	}
	if len(got) != 2 || got["fwd"] || !got["fwd2"] {
		t.Fatalf("landing_pending_delete by line = %v", got)
	}
}
