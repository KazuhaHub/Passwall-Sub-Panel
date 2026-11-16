package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
)

type visibilityNodes struct {
	ports.NodeRepo
	node *domain.Node
}

func (r visibilityNodes) GetByID(context.Context, int64) (*domain.Node, error) {
	if r.node == nil {
		return nil, domain.ErrNotFound
	}
	copied := *r.node
	return &copied, nil
}

type visibilityGroups struct {
	ports.GroupRepo
	groups []*domain.Group
	err    error
}

func (r visibilityGroups) List(context.Context) ([]*domain.Group, error) { return r.groups, r.err }

type recordingPreviewer struct {
	draft  *domain.Node
	result *group.LineVisibility
	err    error
	saved  []int64
	stored *domain.Node
}

func (p *recordingPreviewer) GroupsForNode(_ context.Context, n *domain.Node) ([]int64, error) {
	p.stored = n
	return p.saved, p.err
}

func (p *recordingPreviewer) GroupsForSeparator(context.Context, domain.SeparatorEntry) ([]int64, error) {
	return nil, p.err
}

func (p *recordingPreviewer) List(context.Context) ([]*domain.Group, error) { return nil, p.err }

func (p *recordingPreviewer) PreviewLineVisibility(_ context.Context, draft *domain.Node) (*group.LineVisibility, error) {
	p.draft = draft
	return p.result, p.err
}

func postLineVisibility(t *testing.T, h *AdminNodeHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/nodes/:id/line-visibility", h.PreviewLineVisibility)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/nodes/1/line-visibility", bytes.NewBufferString(body)))
	return w
}

func storedVisibilityNode() *domain.Node {
	return &domain.Node{ID: 1, PanelID: 3, ServerAddress: "land.example", Region: "HK", Tags: []string{"old"},
		Relays: []domain.RelayLine{{ID: "saved", Address: "saved.example", Region: "ZZ"}}}
}

// The draft replaces only labels and lines; everything else is the stored
// node, and the line labels are normalized exactly as a save would.
func TestPreviewLineVisibilityBuildsDraftFromStoredNode(t *testing.T) {
	previewer := &recordingPreviewer{result: &group.LineVisibility{Groups: []*domain.Group{{ID: 4, Name: "VIP"}}, NodeGroups: []int64{4}, Relays: [][]int64{{4}}, Routes: [][]int64{{}}}, saved: []int64{4, 9}}
	h := NewAdminNodeHandler(node.New(visibilityNodes{node: storedVisibilityNode()}, nil, nil, nil, nil, nil, nil), nil, nil, nil, nil, nil, nil).WithLineVisibility(previewer)
	w := postLineVisibility(t, h, `{"region":" JP ","tags":[" edge ",""],"relays":[{"id":"gz","region":" cn ","tags":["b","a","b"]}],"routes":[{"id":"r","entry_relay_id":"gz","region":"US","tags":[]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	d := previewer.draft
	if d.PanelID != 3 || d.ServerAddress != "land.example" || d.Region != "JP" || !reflect.DeepEqual(d.Tags, []string{"edge"}) {
		t.Fatalf("draft node %+v", d)
	}
	if len(d.Relays) != 1 || d.Relays[0].ID != "gz" || d.Relays[0].Region != "cn" || !reflect.DeepEqual(d.Relays[0].Tags, []string{"b", "a"}) {
		t.Fatalf("draft relays %+v", d.Relays)
	}
	if len(d.RouteState.Lines) != 1 || d.RouteState.Lines[0].EntryRelayID != "gz" || d.RouteState.Lines[0].Region != "US" {
		t.Fatalf("draft routes %+v", d.RouteState.Lines)
	}
	var body lineVisibilityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body.Groups, []lineVisibilityGroup{{ID: 4, Name: "VIP"}}) || !reflect.DeepEqual(body.Relays, [][]int64{{4}}) || body.Routes[0] == nil {
		t.Fatalf("response %s", w.Body.String())
	}
	// ADR 0042 §2: the groups the STORED node passes, so the editor can name
	// those the save would remove it from.
	if !reflect.DeepEqual(body.SavedNodeGroups, []int64{4, 9}) || previewer.stored == nil || previewer.stored.Region != "HK" {
		t.Fatalf("saved node groups %v from %+v", body.SavedNodeGroups, previewer.stored)
	}
}

// End to end with the real group service: a group the node passes but the
// relay does not is listed for the node and missing for the relay.
func TestPreviewLineVisibilityWithGroupService(t *testing.T) {
	groups := group.New(visibilityGroups{groups: []*domain.Group{
		{ID: 1, Name: "HK", TagFilter: domain.TagFilter{Tags: []string{"region:HK"}}},
		{ID: 2, Name: "Any", TagFilter: domain.TagFilter{All: true}},
	}}, nil, nil)
	h := NewAdminNodeHandler(node.New(visibilityNodes{node: storedVisibilityNode()}, nil, nil, nil, nil, nil, nil), nil, nil, nil, nil, nil, nil).WithLineVisibility(groups)
	w := postLineVisibility(t, h, `{"region":"HK","tags":[],"relays":[{"region":"CN","tags":[]},{"region":"hk","tags":[]}],"routes":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body lineVisibilityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body.NodeGroups, []int64{1, 2}) || !reflect.DeepEqual(body.Relays, [][]int64{{2}, {1, 2}}) || !reflect.DeepEqual(body.Routes, [][]int64{}) {
		t.Fatalf("response %s", w.Body.String())
	}
	// Moving the draft to JP drops the HK group; the saved answer still has it.
	w = postLineVisibility(t, h, `{"region":"JP","tags":[],"relays":[],"routes":[]}`)
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body.NodeGroups, []int64{2}) || !reflect.DeepEqual(body.SavedNodeGroups, []int64{1, 2}) {
		t.Fatalf("draft %v saved %v", body.NodeGroups, body.SavedNodeGroups)
	}
}

func TestPreviewLineVisibilityFailuresAreNotAnswers(t *testing.T) {
	stored := storedVisibilityNode()
	newHandler := func(p NodeEditorPreviewer, n *domain.Node) *AdminNodeHandler {
		h := NewAdminNodeHandler(node.New(visibilityNodes{node: n}, nil, nil, nil, nil, nil, nil), nil, nil, nil, nil, nil, nil)
		if p != nil {
			h.WithLineVisibility(p)
		}
		return h
	}
	if w := postLineVisibility(t, newHandler(nil, stored), `{}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired preview: %d", w.Code)
	}
	if w := postLineVisibility(t, newHandler(&recordingPreviewer{err: errors.New("db down")}, stored), `{}`); w.Code < 500 {
		t.Fatalf("a failed read must not look like an answer: %d %s", w.Code, w.Body.String())
	}
	if w := postLineVisibility(t, newHandler(&recordingPreviewer{}, nil), `{}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown node: %d", w.Code)
	}
	long := `{"relays":[{"region":"` + string(bytes.Repeat([]byte("x"), domain.MaxLineRegionBytes+1)) + `"}]}`
	if w := postLineVisibility(t, newHandler(&recordingPreviewer{}, stored), long); w.Code != http.StatusBadRequest {
		t.Fatalf("over-long label: %d", w.Code)
	}
}
