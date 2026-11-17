package handler

import (
	"context"
	"errors"
	"fmt"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/realitykey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
)

func strPtr(s string) *string { return &s }

func TestParseRelaysReadsTheModeAndLeavesTheSavedOneAlone(t *testing.T) {
	lines, err := parseRelays([]relayLineDTO{
		{Address: "a.example", Port: 20001, EntryPanelID: 9, Enabled: true},                          // mode omitted: saved
		{Address: "b.example", EntryPanelID: 9, Enabled: true, Mode: strPtr("tunnel")},               // a tunnel line needs no port of its own
		{Address: "c.example", Port: 20002, EntryPanelID: 9, Enabled: true, Mode: strPtr("forward")}, // explicit forward
	})
	if err != nil {
		t.Fatal(err)
	}
	if !lines[0].ModeUnresolved || lines[0].Tunnel() {
		t.Fatalf("an omitted mode must stay unresolved: %+v", lines[0])
	}
	if !lines[1].Tunnel() || lines[1].ModeUnresolved {
		t.Fatalf("tunnel line = %+v", lines[1])
	}
	if lines[2].Tunnel() || lines[2].ModeUnresolved {
		t.Fatalf("forward line = %+v", lines[2])
	}
}

func TestParseRelaysRejectsABadModeAndAForwardWithoutAPort(t *testing.T) {
	if _, err := parseRelays([]relayLineDTO{{Address: "a", EntryPanelID: 9, Port: 1, Mode: strPtr("mux")}}); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("err = %v", err)
	}
	if _, err := parseRelays([]relayLineDTO{{Address: "a", EntryPanelID: 9, Mode: strPtr("forward")}}); err == nil || !strings.Contains(err.Error(), "entry port") {
		t.Fatalf("err = %v", err)
	}
}

func TestATunnelLineIsReportedWithItsListenersOwnPortAndKeys(t *testing.T) {
	child := &domain.Node{ID: 20, PanelID: 9, DisplayName: "JP · hk", Enabled: true, DesiredProtocol: "vless", DesiredPort: 24443, Flow: "xtls-rprx-vision",
		ConfigSyncState: domain.ConfigSyncSynced, EgressPanelID: 7, EgressReady: true, ParentNodeID: 1, ParentLineID: "t",
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"target":"www.asus.com:443","serverNames":["www.asus.com"],"shortIds":["ab"],` +
			`"privateKey":"secret-key","settings":{"publicKey":"pub-key","fingerprint":"chrome"}}}`}
	lines := []domain.RelayLine{{ID: "t", Name: "hk", Address: "hk.example", Enabled: true, EntryPanelID: 9, Mode: domain.RelayModeTunnel,
		TunnelNodeID: 20, TunnelNode: child, ManagedReady: true, Tags: []string{}}}

	operator := relayDTOs(lines)[0]
	if operator.Mode == nil || *operator.Mode != "tunnel" || operator.Port != 24443 || !operator.Running || !operator.ManagedReady || operator.Tunnel == nil {
		t.Fatalf("dto = %+v", operator)
	}
	tunnel := operator.Tunnel
	if tunnel.NodeID != 20 || !tunnel.Applied || !tunnel.EgressReady || tunnel.Security != "reality" || tunnel.Target != "www.asus.com:443" ||
		len(tunnel.ServerNames) != 1 || tunnel.PublicKey != "pub-key" || tunnel.Fingerprint != "chrome" || tunnel.ShortIDs[0] != "ab" {
		t.Fatalf("tunnel = %+v", tunnel)
	}
	if tunnel.PrivateKey != "" {
		t.Fatal("an operator was handed the REALITY private key")
	}
	if admin := relayDTOsFor(lines, true)[0].Tunnel; admin.PrivateKey != "secret-key" {
		t.Fatalf("an administrator did not get the private key: %+v", admin)
	}
	forward := relayDTOs([]domain.RelayLine{{ID: "f", Address: "a", Port: 20001, Enabled: true, EntryPanelID: 9, Tags: []string{}}})[0]
	if forward.Mode != nil || forward.Tunnel != nil || forward.Port != 20001 {
		t.Fatalf("a forward is reported as %+v", forward)
	}
}

// A confirmation the service asks for is a 409 whose parameters list what the
// action creates and removes, so the editor can say it in its own words.
func TestATunnelConfirmationIsA409WithTheListeners(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	err := &front.Refusal{Code: front.CodeConfirmRequired, Message: "confirm", Params: map[string]any{
		"kind": "tunnel", "action": "switch_mode", "removes": []node.TunnelItem{{NodeID: 20, Name: "JP · hk", Port: 24443}},
	}}
	mapNodeServiceError(c, err)
	body := recorder.Body.String()
	if recorder.Code != http.StatusConflict || !strings.Contains(body, `"code":"confirm_required"`) || !strings.Contains(body, `"kind":"tunnel"`) ||
		!strings.Contains(body, `"port":24443`) || !strings.Contains(body, "JP · hk") {
		t.Fatalf("%d %s", recorder.Code, body)
	}
}

func TestDeriveRealityPublicKeyAnswersTheKeysPairOrRefuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	priv, pub, err := realitykey.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	h := &AdminNodeHandler{}
	call := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h.DeriveRealityPublicKey(c)
		return rec
	}
	if rec := call(`{"private_key":"` + priv + `"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), pub) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := call(`{"private_key":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed key answered %d", rec.Code)
	}
}

func TestDeletingAServerNamesTheTunnelLinesThatEnterThroughIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	landing := &domain.Node{ID: 1, PanelID: 7, DisplayName: "JP", Relays: []domain.RelayLine{{ID: "t", Name: "Hong Kong", Address: "hk", Mode: domain.RelayModeTunnel, TunnelNodeID: 2}}}
	child := &domain.Node{ID: 2, PanelID: 9, DisplayName: "JP · Hong Kong", ParentNodeID: 1, ParentLineID: "t"}
	h := &AdminServersHandler{nodes: staticNodeLister{nodes: []*domain.Node{landing, child}}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: "9"}}
	h.Delete(c)
	body := rec.Body.String()
	if rec.Code != http.StatusConflict || !strings.Contains(body, `"code":"server_has_nodes"`) || !strings.Contains(body, `"tunnel_line":"Hong Kong"`) ||
		!strings.Contains(body, `"tunnel_line_of":"JP"`) || !strings.Contains(body, "remove those lines") {
		t.Fatalf("%d %s", rec.Code, body)
	}
}

type staticNodeLister struct {
	ports.NodeRepo
	nodes []*domain.Node
}

func (s staticNodeLister) List(context.Context) ([]*domain.Node, error) { return s.nodes, nil }

// A tunnel whose promised port was taken is a 409 the admin can retry, from every
// handler that makes tunnels (they share the node error mapper).
func TestAPromisedLinkPortThatWasTakenIsA409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	mapNodeServiceError(c, fmt.Errorf("link 9->3: %w", transit.ErrReservedPortTaken))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"link_port_taken"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !errors.Is(transit.ErrReservedPortTaken, domain.ErrConflict) {
		t.Fatal("the error does not read as a conflict elsewhere")
	}
}
