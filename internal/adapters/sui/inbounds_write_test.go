package sui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/nodespec"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestAddInboundConvertsCanonicalRealityToSUI(t *testing.T) {
	var tlsName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/inbounds":
			writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{"id": 1, "tag": "edge"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/apiv2/save":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			switch r.Form.Get("object") + "/" + r.Form.Get("action") {
			case "tls/new":
				var model tlsModel
				if err := json.Unmarshal([]byte(r.Form.Get("data")), &model); err != nil {
					t.Fatalf("decode TLS: %v", err)
				}
				tlsName = model.Name
				if !strings.HasPrefix(tlsName, managedTLSPrefix) {
					t.Errorf("TLS name = %q", tlsName)
				}
				var inTLS, outTLS map[string]any
				_ = json.Unmarshal(model.Server, &inTLS)
				_ = json.Unmarshal(model.Client, &outTLS)
				reality := mapValue(inTLS["reality"])
				clientReality := mapValue(outTLS["reality"])
				if !boolValue(reality["enabled"]) || stringValue(reality["private_key"]) != "private-key" || stringValue(clientReality["public_key"]) != "public-key" {
					t.Errorf("TLS reality server=%#v client=%#v", inTLS, outTLS)
				}
				writeObject(t, w, map[string]any{"tls": []any{map[string]any{"id": 12, "name": tlsName, "server": inTLS, "client": outTLS}}})
			case "inbounds/new":
				if r.Form.Get("initUsers") != "0" {
					t.Errorf("initUsers = %q", r.Form.Get("initUsers"))
				}
				var inbound map[string]any
				if err := json.Unmarshal([]byte(r.Form.Get("data")), &inbound); err != nil {
					t.Fatalf("decode inbound: %v", err)
				}
				if inbound["type"] != "vless" || inbound["tag"] != "edge (2)" || intValue(inbound["tls_id"]) != 12 {
					t.Errorf("inbound = %#v", inbound)
				}
				transport := mapValue(inbound["transport"])
				if transport["type"] != "ws" || transport["path"] != "/ws" {
					t.Errorf("transport = %#v", transport)
				}
				if _, leaked := inbound["streamSettings"]; leaked {
					t.Error("Xray streamSettings leaked into S-UI payload")
				}
				writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{"id": 9, "tag": "edge (2)"}}})
			default:
				t.Fatalf("unexpected save: %s/%s", r.Form.Get("object"), r.Form.Get("action"))
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	id, err := testClient(server).AddInbound(context.Background(), ports.InboundSpec{
		Remark: "edge", Enable: true, Listen: "::", Port: 443, Protocol: "vless",
		Settings: `{"clients":[],"decryption":"none"}`,
		StreamSettings: `{
			"network":"ws","security":"reality","wsSettings":{"path":"/ws"},
			"realitySettings":{"dest":"origin.example.com:443","serverNames":["edge.example.com"],"privateKey":"private-key","shortIds":["01"],"settings":{"publicKey":"public-key","fingerprint":"chrome"}}
		}`,
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	if id != 9 || tlsName == "" {
		t.Fatalf("id=%d tlsName=%q", id, tlsName)
	}
}

func TestUpdateInboundSwapsAndCleansManagedTLS(t *testing.T) {
	var newTLSName string
	var deletedTLS int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/inbounds" && r.URL.Query().Get("id") == "7":
			writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{"id": 7, "type": "trojan", "tag": "old", "tls_id": 2}}})
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/inbounds":
			writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{"id": 7, "tag": "old", "tls_id": 12}}})
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/tls":
			writeObject(t, w, map[string]any{"tls": []any{
				map[string]any{"id": 2, "name": managedTLSPrefix + " old", "server": map[string]any{}, "client": map[string]any{}},
				map[string]any{"id": 12, "name": newTLSName, "server": map[string]any{}, "client": map[string]any{}},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/apiv2/save":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			switch r.Form.Get("object") + "/" + r.Form.Get("action") {
			case "tls/new":
				var model tlsModel
				_ = json.Unmarshal([]byte(r.Form.Get("data")), &model)
				newTLSName = model.Name
				writeObject(t, w, map[string]any{"tls": []any{map[string]any{"id": 12, "name": newTLSName, "server": json.RawMessage(model.Server), "client": json.RawMessage(model.Client)}}})
			case "inbounds/edit":
				var inbound map[string]any
				_ = json.Unmarshal([]byte(r.Form.Get("data")), &inbound)
				if intValue(inbound["id"]) != 7 || intValue(inbound["tls_id"]) != 12 || inbound["tag"] != "renamed" {
					t.Errorf("edited inbound = %#v", inbound)
				}
				writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{"id": 7, "tag": "renamed", "tls_id": 12}}})
			case "tls/del":
				_ = json.Unmarshal([]byte(r.Form.Get("data")), &deletedTLS)
				writeObject(t, w, nil)
			default:
				t.Fatalf("unexpected save: %s/%s", r.Form.Get("object"), r.Form.Get("action"))
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	err := testClient(server).UpdateInbound(context.Background(), 7, ports.InboundSpec{
		Remark: "renamed", Enable: true, Port: 443, Protocol: "trojan",
		StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"edge.example.com","certificates":[{"certificateFile":"/cert.pem","keyFile":"/key.pem"}]}}`,
	})
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if deletedTLS != 2 {
		t.Fatalf("deleted TLS = %d", deletedTLS)
	}
}

func TestValidateSUIInboundRequestRejectsUnmappedSemantics(t *testing.T) {
	tests := []ports.InboundSpec{
		{Enable: false, Port: 443, Protocol: "vless"},
		{Enable: true, Port: 443, Protocol: "vless", ExpiryTime: 1},
		{Enable: true, Port: 443, Protocol: "vless", Allocate: `{"strategy":"always"}`},
		{Enable: true, Port: 443, Protocol: "vless", Sniffing: `{"enabled":true,"destOverride":["tls"]}`},
		{Enable: true, Port: 443, Protocol: "vless", StreamSettings: `{"sockopt":{"acceptProxyProtocol":true}}`},
	}
	for _, input := range tests {
		spec, err := nodespec.Decode(input)
		if err != nil {
			t.Fatalf("Decode(%#v): %v", input, err)
		}
		if err := validateSUIInboundRequest(input, spec); err == nil {
			t.Fatalf("validateSUIInboundRequest(%#v) succeeded", input)
		}
	}
}

func TestCreateManagedTLSRecoversCommittedSaveAfterBadResponse(t *testing.T) {
	var name string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/apiv2/save":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			var model tlsModel
			if err := json.Unmarshal([]byte(r.Form.Get("data")), &model); err != nil {
				t.Fatal(err)
			}
			name = model.Name
			_, _ = w.Write([]byte(`{"success":`))
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/tls":
			writeObject(t, w, map[string]any{"tls": []any{map[string]any{
				"id": 42, "name": name, "server": map[string]any{}, "client": map[string]any{},
			}}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	id, err := testClient(server).createManagedTLS(context.Background(), "edge", nodespec.Security{
		Mode: "tls", TLS: nodespec.TLS{ServerName: "edge.example.com", CertificatePath: "/cert.pem", KeyPath: "/key.pem"},
	})
	if err != nil {
		t.Fatalf("createManagedTLS: %v", err)
	}
	if id != 42 || name == "" {
		t.Fatalf("id=%d name=%q", id, name)
	}
}

func TestDelInboundUsesTagAndDoesNotDeleteUserTLS(t *testing.T) {
	var deleted any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/inbounds":
			writeObject(t, w, map[string]any{"inbounds": []any{map[string]any{"id": 7, "tag": "edge", "tls_id": 5}}})
		case r.Method == http.MethodPost && r.URL.Path == "/apiv2/save":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("object") != "inbounds" || r.Form.Get("action") != "del" {
				t.Fatalf("unexpected save: %s/%s", r.Form.Get("object"), r.Form.Get("action"))
			}
			_ = json.Unmarshal([]byte(r.Form.Get("data")), &deleted)
			writeObject(t, w, nil)
		case r.Method == http.MethodGet && r.URL.Path == "/apiv2/tls":
			writeObject(t, w, map[string]any{"tls": []any{map[string]any{"id": 5, "name": "hand-managed", "server": map[string]any{}, "client": map[string]any{}}}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	if err := testClient(server).DelInbound(context.Background(), 7); err != nil {
		t.Fatalf("DelInbound: %v", err)
	}
	if deleted != "edge" {
		t.Fatalf("deleted value = %#v", deleted)
	}
}

func TestSUIInboundCapabilitiesAreGranular(t *testing.T) {
	client := &Client{}
	want := map[ports.PanelCapability]bool{
		ports.CapabilityInboundCreate: true,
		ports.CapabilityInboundUpdate: true,
		ports.CapabilityInboundDelete: true,
	}
	for _, capability := range client.Capabilities() {
		delete(want, capability)
		if capability == ports.CapabilityInboundWrite || capability == ports.CapabilityInboundEnable {
			t.Fatalf("S-UI advertised unsupported capability %q", capability)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing capabilities: %#v", want)
	}
	if err := client.SetInboundEnable(context.Background(), 1, true); !errors.Is(err, ports.ErrPanelCapabilityUnsupported) {
		t.Fatalf("SetInboundEnable error = %v", err)
	}
}

func TestShadowsocksBothNetworksUsesSingBoxDefault(t *testing.T) {
	spec, err := nodespec.Decode(ports.InboundSpec{
		Port: 8388, Protocol: "shadowsocks",
		Settings: `{"method":"2022-blake3-aes-128-gcm","password":"secret","network":"tcp,udp"}`,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	body, err := suiInboundFromSpec(spec, "ss", 0)
	if err != nil {
		t.Fatalf("suiInboundFromSpec: %v", err)
	}
	if _, exists := body["network"]; exists {
		t.Fatalf("both-network Shadowsocks must omit sing-box network: %#v", body)
	}
}

func TestSUIModernProtocolPayloads(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		settings string
		check    func(*testing.T, map[string]any)
	}{
		{
			name: "anytls", protocol: "anytls", settings: `{"padding_scheme":["stop=8","0=30-30"]}`,
			check: func(t *testing.T, body map[string]any) {
				padding, ok := body["padding_scheme"].([]string)
				if !ok || len(padding) != 2 || padding[0] != "stop=8" {
					t.Fatalf("padding_scheme = %#v", body["padding_scheme"])
				}
			},
		},
		{
			name: "tuic", protocol: "tuic", settings: `{"congestion_control":"bbr","auth_timeout":"4s","zero_rtt_handshake":true,"heartbeat":"12s"}`,
			check: func(t *testing.T, body map[string]any) {
				if body["congestion_control"] != "bbr" || body["auth_timeout"] != "4s" ||
					body["zero_rtt_handshake"] != true || body["heartbeat"] != "12s" {
					t.Fatalf("TUIC body = %#v", body)
				}
			},
		},
		{
			name: "naive", protocol: "naive", settings: `{"network":"udp","quic_congestion_control":"bbr2_variant"}`,
			check: func(t *testing.T, body map[string]any) {
				if body["network"] != "udp" || body["quic_congestion_control"] != "bbr2_variant" {
					t.Fatalf("Naive body = %#v", body)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := nodespec.Decode(ports.InboundSpec{
				Enable: true, Port: 443, Protocol: tc.protocol, Settings: tc.settings,
				StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"edge.example.com","certificates":[{"certificateFile":"/cert.pem","keyFile":"/key.pem"}]}}`,
			})
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			body, err := suiInboundFromSpec(spec, tc.name, 9)
			if err != nil {
				t.Fatalf("suiInboundFromSpec: %v", err)
			}
			if body["type"] != tc.protocol || intValue(body["tls_id"]) != 9 {
				t.Fatalf("base body = %#v", body)
			}
			tc.check(t, body)
		})
	}
}

func TestSUIModernProtocolsRequireTLS(t *testing.T) {
	for _, protocol := range []string{"anytls", "tuic", "naive"} {
		spec, err := nodespec.Decode(ports.InboundSpec{Enable: true, Port: 443, Protocol: protocol})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := suiInboundFromSpec(spec, protocol, 0); err == nil {
			t.Fatalf("%s without TLS was accepted", protocol)
		}
	}
}
