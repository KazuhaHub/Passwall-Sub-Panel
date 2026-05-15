package xui

import (
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestUpdateClientInSettingsMatchesByEmailWhenIDIsEmpty(t *testing.T) {
	settings := `{
		"method": "2022-blake3-aes-256-gcm",
		"password": "server-psk",
		"network": "tcp,udp",
		"clients": [
			{"id": "", "email": "u24-n7@example.test", "enable": true, "password": "old", "expiryTime": 0, "comment": "keep"}
		]
	}`

	got, err := updateClientInSettings(settings, "340c1b7e-8434-4cd3-a6bf-5a44a9751f36", ports.ClientSpec{
		ID:         "340c1b7e-8434-4cd3-a6bf-5a44a9751f36",
		Email:      "u24-n7@example.test",
		Enable:     false,
		Password:   "derived",
		ExpiryTime: 1770000000000,
	})
	if err != nil {
		t.Fatal(err)
	}

	var decoded struct {
		Method  string           `json:"method"`
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != "2022-blake3-aes-256-gcm" {
		t.Fatalf("method = %q", decoded.Method)
	}
	client := decoded.Clients[0]
	if client["id"] != "340c1b7e-8434-4cd3-a6bf-5a44a9751f36" {
		t.Fatalf("id = %#v", client["id"])
	}
	if client["enable"] != false {
		t.Fatalf("enable = %#v", client["enable"])
	}
	if client["password"] != "derived" {
		t.Fatalf("password = %#v", client["password"])
	}
	if client["comment"] != "keep" {
		t.Fatalf("existing field was not preserved: %#v", client)
	}
}

func TestReplaceSettingsClientsPreservesCurrentClients(t *testing.T) {
	next := `{"method":"2022-blake3-aes-256-gcm","clients":[]}`
	current := `{"method":"old","clients":[{"id":"a","email":"a@example.test"}]}`

	got, err := replaceSettingsClients(next, current)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Method  string `json:"method"`
		Clients []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != "2022-blake3-aes-256-gcm" {
		t.Fatalf("method = %q", decoded.Method)
	}
	if len(decoded.Clients) != 1 || decoded.Clients[0].Email != "a@example.test" {
		t.Fatalf("clients not preserved: %#v", decoded.Clients)
	}
}

func TestDecodeTrafficObjAcceptsSingleObject(t *testing.T) {
	raw := json.RawMessage(`{"id":9,"inboundId":4,"email":"u25-n2@psp.local","up":1048576,"down":933232640,"total":934281216,"enable":true}`)

	got, err := decodeTrafficObj(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Email != "u25-n2@psp.local" || got[0].Total != 934281216 {
		t.Fatalf("unexpected traffic: %#v", got[0])
	}
}

func TestDecodeTrafficObjAcceptsArray(t *testing.T) {
	raw := json.RawMessage(`[{"id":1,"inboundId":2,"email":"a","up":1,"down":2,"total":3}]`)

	got, err := decodeTrafficObj(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Total != 3 {
		t.Fatalf("unexpected traffic: %#v", got)
	}
}
