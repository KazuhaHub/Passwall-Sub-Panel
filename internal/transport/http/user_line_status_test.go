package http

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

type statusGroups struct{ ports.GroupRepo }

func (statusGroups) GetByID(context.Context, int64) (*domain.Group, error) {
	return &domain.Group{ID: 1, TagFilter: domain.TagFilter{Tags: []string{"mine"}}}, nil
}

type statusNodes struct {
	ports.NodeRepo
	nodes []*domain.Node
}

func (r statusNodes) ListEnabled(context.Context) ([]*domain.Node, error) { return r.nodes, nil }

type statusPanels struct{ ports.XUIPanelRepo }

func (statusPanels) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	return &domain.XUIPanel{ID: 1, Kind: domain.PanelKind3XUI, URL: "https://private-panel.example"}, nil
}

type statusRelayResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Only   bool   `json:"only_line_name"`
}
type statusNodeResponse struct {
	Name       string                `json:"name"`
	ShowDirect bool                  `json:"show_direct"`
	Relays     []statusRelayResponse `json:"relay_statuses"`
}

func TestUserServerStatusSharesRelayPublicationAndGroupScope(t *testing.T) {
	u := &domain.User{ID: 1, UPN: "subscriber", Role: domain.RoleUser, Enabled: true, GroupID: 1}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: u}}
	n := &domain.Node{ID: 1, PanelID: 1, InboundID: 11, Enabled: true, DisplayName: "Mine", Tags: []string{"mine"}, DesiredProtocol: "vless", DesiredPort: 443,
		HealthState: domain.NodeHealthOK,
		// Lines carry the labels copied from the node at creation (ADR 0040);
		// the group's tag_filter holds on them too (ADR 0041).
		Relays: []domain.RelayLine{
			{ID: "front", Name: "Front", Address: "private-relay.example", Enabled: true, OnlyLineName: true, Tags: []string{"mine"}},
			{ID: "second", Name: "Second", Address: "second-relay.example", Enabled: true, Tags: []string{"mine"}},
		},
		RelayHealth: []domain.RelayHealth{{Index: 0, Address: "private-relay.example", Port: 443, State: domain.NodeHealthUnreachable}},
	}
	other := *n
	other.ID, other.DisplayName, other.Tags = 2, "Other tenant", []string{"other"}
	repos := ports.Repos{User: users, Group: statusGroups{}, Node: statusNodes{nodes: []*domain.Node{n, &other}}, XUIPanel: statusPanels{}, Settings: &dispatchSettingsRepo{}}
	groups := group.New(repos.Group, repos.Node, nil)
	issuer := jwtutil.NewIssuer(strings.Repeat("status-test-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "line-status-test"}
	})
	authSvc := auth.New(issuer)
	token, _, err := authSvc.IssueTokens(u)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{Cfg: &config.Config{ConfigDir: t.TempDir()}, Repos: repos, Auth: authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil), Group: groups, Render: render.New(repos, nil, groups)})
	read := func() []statusNodeResponse {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/user/me/server-status", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		for _, secret := range []string{"private-panel.example", "private-relay.example", "second-relay.example", "Other tenant", "12345678", "entry_relay_id", "outbound_tag"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("user status leaked %q", secret)
			}
		}
		var body struct {
			Nodes []statusNodeResponse `json:"nodes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Nodes
	}
	got := read()
	if len(got) != 1 || len(got[0].Relays) != 2 || got[0].Relays[0].Status != "down" || !got[0].Relays[0].Only || got[0].Relays[1].Status != "unknown" {
		t.Fatalf("unexpected status: %+v", got)
	}
	n.HideDirect = true
	got = read()
	if len(got) != 1 || got[0].ShowDirect || len(got[0].Relays) != 2 {
		t.Fatalf("hiding the standalone direct line affected its relays: %+v", got)
	}
	n.Relays[0].Enabled, n.Relays[1].Enabled = false, false
	if len(read()) != 0 {
		t.Fatal("entirely hidden node still displayed")
	}
	// ADR 0041: a relay whose own tags no longer satisfy the group is not
	// listed. The status page shares the subscription's set.
	n.HideDirect, n.Relays[0].Enabled, n.Relays[1].Enabled = false, true, true
	n.Relays[0].Tags = []string{"elsewhere"}
	got = read()
	if len(got) != 1 || len(got[0].Relays) != 1 || got[0].Relays[0].Name != "Second" {
		t.Fatalf("hidden relay still listed: %+v", got)
	}
}
