package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
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
