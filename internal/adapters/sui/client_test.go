package sui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func testClient(server *httptest.Server) *Client {
	return &Client{panelName: "test-sui", baseURL: server.URL, token: "secret", http: server.Client()}
}

func writeObject(t *testing.T, w http.ResponseWriter, obj any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "msg": "", "obj": obj}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestNewRequiresTokenAuthentication(t *testing.T) {
	if _, err := New(&domain.Panel{URL: "https://sui.example.test"}); err == nil {
		t.Fatal("missing token accepted")
	}
	if _, err := New(&domain.Panel{URL: "https://sui.example.test", APIToken: "token", AuthMethod: domain.XUIAuthPassword}); err == nil {
		t.Fatal("password authentication accepted")
	}
	client, err := New(&domain.Panel{URL: "https://sui.example.test/app/", APIToken: "token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.baseURL != "https://sui.example.test/app" {
		t.Fatalf("base URL = %q", client.baseURL)
	}
}

func TestAddClientUsesTokenAndSaveContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apiv2/save" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Token"); got != "secret" {
			t.Errorf("Token = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("object") != "clients" || r.Form.Get("action") != "new" {
			t.Errorf("save selector = %q/%q", r.Form.Get("object"), r.Form.Get("action"))
		}
		var model clientModel
		if err := json.Unmarshal([]byte(r.Form.Get("data")), &model); err != nil {
			t.Errorf("decode data: %v", err)
		}
		if model.Name != "u1@psp.local" || !reflect.DeepEqual(model.Inbounds, []int{3, 5}) {
			t.Errorf("client model = %#v", model)
		}
		if got := model.Config["vless"]["uuid"]; got != "uuid-1" {
			t.Errorf("vless uuid = %#v", got)
		}
		if got := model.Config["trojan"]["password"]; got != "pw-1" {
			t.Errorf("trojan password = %#v", got)
		}
		writeObject(t, w, nil)
	}))
	defer server.Close()

	err := testClient(server).AddClientToInbounds(context.Background(), []int{3, 5}, ports.ClientSpec{
		ID: "uuid-1", Email: "u1@psp.local", Enable: true, Password: "pw-1", Auth: "uuid-1",
	})
	if err != nil {
		t.Fatalf("AddClientToInbounds: %v", err)
	}
}

func TestAttachLoadsFullClientBeforeBulkEdit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/clients" && r.URL.Query().Get("id") == "":
			writeObject(t, w, map[string]any{"clients": []any{map[string]any{
				"id": 4, "name": "u1@psp.local", "enable": true, "inbounds": []int{1},
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/clients" && r.URL.Query().Get("id") == "4":
			writeObject(t, w, map[string]any{"clients": []any{map[string]any{
				"id": 4, "name": "u1@psp.local", "enable": true, "inbounds": []int{1},
				"config": map[string]any{"vless": map[string]any{"name": "u1@psp.local", "uuid": "uuid-1"}},
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/apiv2/save":
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm: %v", err)
			}
			if r.Form.Get("action") != "editbulk" {
				t.Errorf("action = %q", r.Form.Get("action"))
			}
			var models []clientModel
			if err := json.Unmarshal([]byte(r.Form.Get("data")), &models); err != nil {
				t.Errorf("decode data: %v", err)
			}
			if len(models) != 1 || !reflect.DeepEqual(models[0].Inbounds, []int{1, 2}) {
				t.Errorf("models = %#v", models)
			}
			if got := models[0].Config["vless"]["uuid"]; got != "uuid-1" {
				t.Errorf("full config was not preserved: %#v", models[0].Config)
			}
			writeObject(t, w, nil)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := testClient(server).BulkAttach(context.Background(), []string{"u1@psp.local"}, []int{2})
	if err != nil {
		t.Fatalf("BulkAttach: %v", err)
	}
	if !reflect.DeepEqual(result.Done, []string{"u1@psp.local"}) {
		t.Fatalf("result = %#v", result)
	}
}

func TestListInboundsNormalisesSUIAndTraffic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Token") != "secret" {
			t.Errorf("missing Token header")
		}
		switch {
		case r.URL.Path == "/apiv2/inbounds" && r.URL.Query().Get("id") == "":
			writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{
				"id": 7, "type": "vless", "tag": "edge", "listen": "::", "listen_port": 443, "users": []string{"u1@psp.local"},
			}}})
		case r.URL.Path == "/apiv2/tls":
			writeObject(t, w, map[string]any{"tls": []any{map[string]any{
				"id": 2, "server": map[string]any{"enabled": true, "server_name": []string{"edge.example.com"}, "alpn": []string{"h2"}},
			}}})
		case r.URL.Path == "/apiv2/clients":
			writeObject(t, w, map[string]any{"clients": []any{map[string]any{
				"id": 4, "name": "u1@psp.local", "enable": true, "inbounds": []int{7},
				"up": 11, "down": 13, "totalUp": 17, "totalDown": 19, "expiry": 123, "onlineAt": 456,
			}}})
		case r.URL.Path == "/apiv2/inbounds" && r.URL.Query().Get("id") == "7":
			writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{
				"id": 7, "type": "vless", "tag": "edge", "listen": "::", "listen_port": 443,
				"tls_id": 2, "transport": map[string]any{"type": "ws", "path": "/ws"},
			}}})
		default:
			t.Errorf("unexpected request: %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	items, err := testClient(server).ListInbounds(context.Background())
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(items) != 1 || items[0].ID != 7 || items[0].Protocol != "vless" || items[0].Port != 443 {
		t.Fatalf("inbounds = %#v", items)
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(items[0].StreamSettings), &stream); err != nil {
		t.Fatalf("decode stream settings: %v", err)
	}
	if stream["network"] != "ws" || stream["security"] != "tls" {
		t.Fatalf("stream settings = %#v", stream)
	}
	if len(items[0].ClientStats) != 1 || items[0].ClientStats[0].Total != 60 || items[0].ClientStats[0].LastOnline != 456000 {
		t.Fatalf("traffic = %#v", items[0].ClientStats)
	}
}

func TestNormaliseInboundReadsRealityClientKeys(t *testing.T) {
	serverTLS, _ := json.Marshal(map[string]any{
		"enabled": true, "server_name": "edge.example.com",
		"reality": map[string]any{
			"enabled": true, "handshake": map[string]any{"server": "origin.example.com", "server_port": 443},
			"private_key": "private-key", "short_id": []string{"01"},
		},
	})
	clientTLS, _ := json.Marshal(map[string]any{
		"enabled": true, "server_name": "edge.example.com",
		"reality": map[string]any{"enabled": true, "public_key": "public-key", "short_id": "01"},
		"utls":    map[string]any{"enabled": true, "fingerprint": "firefox"},
	})
	inbound, err := normaliseInbound(
		inboundSummary{ID: 7, Type: "vless", Tag: "edge", ListenPort: 443},
		map[string]any{"tls_id": float64(2)},
		map[int]tlsModel{2: {ID: 2, Server: serverTLS, Client: clientTLS}},
	)
	if err != nil {
		t.Fatalf("normaliseInbound: %v", err)
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(inbound.StreamSettings), &stream); err != nil {
		t.Fatalf("decode stream: %v", err)
	}
	reality := mapValue(stream["realitySettings"])
	settings := mapValue(reality["settings"])
	if stream["security"] != "reality" || settings["publicKey"] != "public-key" || settings["fingerprint"] != "firefox" {
		t.Fatalf("reality settings = %#v", stream)
	}
}
