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

type statusClients struct {
	ports.PSPClientRepo
	applied bool
}

type statusRouteResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Only   bool   `json:"only_line_name"`
}
type statusNodeResponse struct {
	Name       string                `json:"name"`
	ShowDirect bool                  `json:"show_direct"`
	Routes     []statusRouteResponse `json:"route_statuses"`
}

func (statusClients) ListByUser(_ context.Context, id int64) ([]*domain.PSPClient, error) {
	return []*domain.PSPClient{{ID: 1, UserID: id}}, nil
}
func (r *statusClients) ListInbounds(context.Context, int64) ([]domain.PSPClientInbound, error) {
	if !r.applied {
		return nil, nil
	}
	return []domain.PSPClientInbound{{NodeID: 1, AppliedUUID: "12345678-1234-4abc-8def-123456789abc"}}, nil
}

func TestUserServerStatusSharesRoutePublicationGatesAndGroupScope(t *testing.T) {
	u := &domain.User{ID: 1, UPN: "subscriber", Role: domain.RoleUser, Enabled: true, GroupID: 1}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: u}}
	n := &domain.Node{ID: 1, PanelID: 1, InboundID: 11, Enabled: true, DisplayName: "Mine", Tags: []string{"mine"}, DesiredProtocol: "vless", DesiredPort: 443,
		HealthState: domain.NodeHealthOK, Relays: []domain.RelayLine{{ID: "front", Name: "Front", Address: "private-relay.example", Enabled: true, OnlyLineName: true}},
		RelayHealth: []domain.RelayHealth{{Index: 0, Address: "private-relay.example", Port: 443, State: domain.NodeHealthUnreachable}},
		RouteState: domain.NodeRouteState{Status: "applied", Lines: []domain.VlessRouteLine{
			{ID: "direct-route", Name: "D", Route: 1001, Enabled: true},
			{ID: "relay-route", OutboundTag: "warp", EntryRelayID: "front", Route: 1002, Enabled: true, OnlyLineName: true},
		}},
	}
	n.RouteState.Binding = domain.RouteBinding("https://private-panel.example", n)
	other := *n
	other.ID, other.DisplayName, other.Tags = 2, "Other tenant", []string{"other"}
	clients := &statusClients{applied: true}
	repos := ports.Repos{User: users, Group: statusGroups{}, Node: statusNodes{nodes: []*domain.Node{n, &other}}, XUIPanel: statusPanels{}, PSPClient: clients, Settings: &dispatchSettingsRepo{}}
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
		for _, secret := range []string{"private-panel.example", "private-relay.example", "Other tenant", "12345678", "entry_relay_id", "outbound_tag"} {
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
	if len(got) != 1 || len(got[0].Routes) != 2 || got[0].Routes[0].Status != "ok" || got[0].Routes[1].Status != "down" || !got[0].Routes[1].Only {
		t.Fatalf("unexpected status: %+v", got)
	}
	n.HideDirect = true
	got = read()
	if len(got) != 1 || got[0].ShowDirect || len(got[0].Routes) != 2 || got[0].Routes[0].Name != "D" {
		t.Fatalf("hiding the standalone direct line affected its derived route: %+v", got)
	}
	n.Relays[0].Enabled = false
	if got = read(); len(got) != 1 || got[0].ShowDirect || len(got[0].Routes) != 1 || got[0].Routes[0].Name != "D" {
		t.Fatalf("direct-inbound route disappeared without an enabled relay: %+v", got)
	}
	relays := n.Relays
	n.Relays = nil
	if got = read(); len(got) != 1 || got[0].ShowDirect || len(got[0].Routes) != 1 || got[0].Routes[0].Name != "D" {
		t.Fatalf("direct-inbound-only status card disappeared: %+v", got)
	}
	n.Relays = relays
	n.RouteState.Lines[0].Enabled = false
	if len(read()) != 0 {
		t.Fatal("entirely hidden node still displayed")
	}
	n.RouteState.Lines[0].Enabled = true
	n.HideDirect, n.Relays[0].Enabled = false, true
	clients.applied = false
	if got = read(); len(got[0].Routes) != 0 {
		t.Fatal("status routes bypassed applied-credential gate")
	}
}
