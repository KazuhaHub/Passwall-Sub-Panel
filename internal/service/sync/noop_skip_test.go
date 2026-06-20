package sync

import (
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// inbWith builds a *ports.Inbound whose settings JSON carries exactly the given
// client object (the shape xrayspec parses).
func inbWith(client map[string]any) *ports.Inbound {
	b, _ := json.Marshal(map[string]any{"clients": []any{client}})
	return &ports.Inbound{ID: 1, Settings: string(b)}
}

func TestClientUnchanged(t *testing.T) {
	// A VLESS spec PSP would push.
	spec := buildClientSpec(domain.ProtoVLESS, "", "uuid-1", "u1@x", "vision", 1700, 200)
	spec.Enable = true

	matching := map[string]any{
		"id": "uuid-1", "email": "u1@x", "enable": true,
		"flow": "vision", "expiryTime": 1700, "totalGB": 200,
	}

	if !clientUnchanged(inbWith(matching), spec, domain.ProtoVLESS) {
		t.Fatal("identical client must be a no-op (skip the update)")
	}

	// Each single field difference must force an update (return false).
	for name, mut := range map[string]func(map[string]any){
		"enable":  func(m map[string]any) { m["enable"] = false },
		"flow":    func(m map[string]any) { m["flow"] = "" },
		"expiry":  func(m map[string]any) { m["expiryTime"] = 9999 },
		"totalGB": func(m map[string]any) { m["totalGB"] = 1 },
		"id":      func(m map[string]any) { m["id"] = "other" },
	} {
		c := map[string]any{"id": "uuid-1", "email": "u1@x", "enable": true, "flow": "vision", "expiryTime": 1700, "totalGB": 200}
		mut(c)
		if clientUnchanged(inbWith(c), spec, domain.ProtoVLESS) {
			t.Errorf("%s differs → must NOT skip", name)
		}
	}

	// Missing client / nil inbound / parse error → update (never a stale skip).
	if clientUnchanged(inbWith(map[string]any{"email": "someone-else@x"}), spec, domain.ProtoVLESS) {
		t.Error("absent client must not skip")
	}
	if clientUnchanged(nil, spec, domain.ProtoVLESS) {
		t.Error("nil inbound must not skip")
	}
	if clientUnchanged(&ports.Inbound{Settings: "{bad json"}, spec, domain.ProtoVLESS) {
		t.Error("unparseable settings must not skip")
	}
}

func TestClientUnchanged_TrojanComparesPassword(t *testing.T) {
	spec := buildClientSpec(domain.ProtoTrojan, "", "uuid-1", "u1@x", "", 0, 0)
	spec.Enable = true
	if spec.Password == "" {
		t.Fatal("precondition: Trojan spec must carry a password")
	}
	base := map[string]any{"id": "uuid-1", "email": "u1@x", "enable": true, "password": spec.Password}
	if !clientUnchanged(inbWith(base), spec, domain.ProtoTrojan) {
		t.Fatal("matching Trojan password must skip")
	}
	base["password"] = "stale-password"
	if clientUnchanged(inbWith(base), spec, domain.ProtoTrojan) {
		t.Fatal("differing Trojan password must NOT skip")
	}
}

func TestClientUnchanged_Hysteria2AlwaysUpdates(t *testing.T) {
	// Hy2's auth credential isn't represented in the parsed client, so we can
	// never verify a match → must always update (conservative).
	spec := buildClientSpec(domain.ProtoHysteria2, "", "uuid-1", "u1@x", "", 0, 0)
	spec.Enable = true
	full := map[string]any{"id": "uuid-1", "email": "u1@x", "enable": true}
	if clientUnchanged(inbWith(full), spec, domain.ProtoHysteria2) {
		t.Fatal("Hysteria2 must never skip (auth not verifiable)")
	}
}
