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
	// The headroom the spec below was built from. clientUnchanged needs it to
	// size the quota deadband, and passing the spec's own value is what keeps
	// the two consistent — a mismatch here would silently test a band the
	// caller never applies.
	const quotaHeadroom = int64(200)
	// A VLESS spec PSP would push.
	spec := buildClientSpec(domain.ProtoVLESS, "", "uuid-1", "u1@x", "vision", domain.UserLifecycle{Enable: true, ExpiryTime: 1700, QuotaHeadroom: quotaHeadroom}, 0)

	matching := map[string]any{
		"id": "uuid-1", "email": "u1@x", "enable": true,
		"flow": "vision", "expiryTime": 1700, "totalGB": 200,
	}

	if !clientUnchanged(inbWith(matching), spec, domain.ProtoVLESS, quotaHeadroom) {
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
		if clientUnchanged(inbWith(c), spec, domain.ProtoVLESS, quotaHeadroom) {
			t.Errorf("%s differs → must NOT skip", name)
		}
	}

	// Missing client / nil inbound / parse error → update (never a stale skip).
	if clientUnchanged(inbWith(map[string]any{"email": "someone-else@x"}), spec, domain.ProtoVLESS, quotaHeadroom) {
		t.Error("absent client must not skip")
	}
	if clientUnchanged(nil, spec, domain.ProtoVLESS, quotaHeadroom) {
		t.Error("nil inbound must not skip")
	}
	if clientUnchanged(&ports.Inbound{Settings: "{bad json"}, spec, domain.ProtoVLESS, quotaHeadroom) {
		t.Error("unparseable settings must not skip")
	}
}

func TestClientUnchanged_TrojanComparesPassword(t *testing.T) {
	spec := buildClientSpec(domain.ProtoTrojan, "", "uuid-1", "u1@x", "", domain.UserLifecycle{Enable: true, ExpiryTime: 0, QuotaHeadroom: 0}, 0)
	if spec.Password == "" {
		t.Fatal("precondition: Trojan spec must carry a password")
	}
	base := map[string]any{"id": "uuid-1", "email": "u1@x", "enable": true, "password": spec.Password}
	if !clientUnchanged(inbWith(base), spec, domain.ProtoTrojan, 0) {
		t.Fatal("matching Trojan password must skip")
	}
	base["password"] = "stale-password"
	if clientUnchanged(inbWith(base), spec, domain.ProtoTrojan, 0) {
		t.Fatal("differing Trojan password must NOT skip")
	}
}

func TestClientUnchanged_Hysteria2AlwaysUpdates(t *testing.T) {
	// Hy2's auth credential isn't represented in the parsed client, so we can
	// never verify a match → must always update (conservative).
	spec := buildClientSpec(domain.ProtoHysteria2, "", "uuid-1", "u1@x", "", domain.UserLifecycle{Enable: true, ExpiryTime: 0, QuotaHeadroom: 0}, 0)
	full := map[string]any{"id": "uuid-1", "email": "u1@x", "enable": true}
	if clientUnchanged(inbWith(full), spec, domain.ProtoHysteria2, 0) {
		t.Fatal("Hysteria2 must never skip (auth not verifiable)")
	}
}

// The quota deadband on the LEGACY per-node path.
//
// This path shares domain.PanelQuotaWithinBand with the shared-client model
// because the drift that motivates the band is identical in both: a user's
// quota is spread across their clients, so traffic on one lowers every other
// one's intended cap while their own panel counter sits still. Fixing only the
// shared path would leave a twin defect that comes back.
//
// Written after a mutation survived: deleting the band from clientUnchanged and
// restoring plain equality broke nothing in the suite, because every existing
// case here happens to differ by far more than a band.
func TestClientUnchanged_QuotaDeadband(t *testing.T) {
	const GiB = int64(1) << 30
	// A realistic shape rather than a boundary artifact: 100 GiB of headroom
	// on a client whose panel counter already reads 40 GiB. The band is the
	// 1 GiB absolute ceiling here, not the 5% arm (5% of 100 GiB is 5 GiB).
	const headroom = 100 * GiB
	const lifetime = 40 * GiB
	spec := buildClientSpec(domain.ProtoVLESS, "", "uuid-1", "u1@x", "vision",
		domain.UserLifecycle{Enable: true, ExpiryTime: 1700, QuotaHeadroom: headroom}, lifetime)
	want := spec.TotalGB // 140 GiB
	band := domain.PanelQuotaBand(headroom)
	if band != 1*GiB {
		t.Fatalf("precondition: band = %d, want 1 GiB (the ceiling should bind here)", band)
	}

	client := func(totalGB int64) map[string]any {
		return map[string]any{
			"id": "uuid-1", "email": "u1@x", "enable": true,
			"flow": "vision", "expiryTime": 1700, "totalGB": totalGB,
		}
	}

	for _, tc := range []struct {
		name   string
		stored int64
		skip   bool
	}{
		{"exact match", want, true},
		// Generous by less than the band: the accumulated sibling drift the
		// band exists to absorb.
		{"generous, inside the band", want + band - 1, true},
		{"generous, exactly at the band", want + band, true},
		{"generous, one byte past the band", want + band + 1, false},
		// Never tolerated at any size: a cap the panel holds BELOW what PSP
		// intends cuts a paying user off early, and it is how a new billing
		// period or a raised quota reaches the panel.
		{"strict by one byte", want - 1, false},
		{"strict by a lot", want - 50*GiB, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clientUnchanged(inbWith(client(tc.stored)), spec, domain.ProtoVLESS, headroom)
			if got != tc.skip {
				t.Errorf("clientUnchanged(stored=%d, want=%d) = %v, want %v", tc.stored, want, got, tc.skip)
			}
		})
	}

	// The headroom argument must be the one the spec was built from. Handing
	// in a different value silently resizes the band — the exact slip that a
	// mutation of the shared-client call site got away with, because both
	// arguments are int64 quota-ish numbers.
	t.Run("a mismatched headroom does not widen the band", func(t *testing.T) {
		if clientUnchanged(inbWith(client(want+band-1)), spec, domain.ProtoVLESS, 0) {
			t.Error("headroom 0 means unlimited, so the band is 0 — this must NOT skip")
		}
	})
}
