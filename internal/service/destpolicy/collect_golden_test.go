package destpolicy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// HITS-TIER NODES GET BYTE-IDENTICAL POLICIES. Tier B changes how a node that is
// on it is compiled; a node that is not must receive exactly the bytes (and so
// the digest) it received before, or every such node would restart its core on
// the upgrade. The golden file was written by the code that did not know tier B
// (regenerate only deliberately: UPDATE_GOLDEN=1).
func TestCollect_HitsNodesByteIdentical(t *testing.T) {
	type scenario struct {
		name  string
		setup func(h *harness)
		panel domain.AuditCollect
		caps  []string
	}
	block := func(h *harness) {
		h.policy("smtp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "25"} })
		h.publishNow()
	}
	allowOnly := func(h *harness) {
		h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "443"} })
		h.publishNow()
	}
	paused := func(h *harness) {
		block(h)
		if err := h.svc.SetPaused(h.ctx, true); err != nil {
			h.t.Fatal(err)
		}
	}
	scenarios := []scenario{
		{"hits block policy", block, domain.AuditCollectHits, []string{policyCap, hitsCap}},
		{"hits panel on a node that also declares usage", block, domain.AuditCollectHits, []string{policyCap, hitsCap, usageCap}},
		{"default panel", block, "", []string{policyCap, hitsCap}},
		{"usage panel on a node without the usage capability", block, domain.AuditCollectHitsAndUsage, []string{policyCap, hitsCap}},
		{"recording off", block, domain.AuditCollectOff, []string{policyCap, hitsCap, usageCap}},
		{"node cannot record", block, domain.AuditCollectHits, []string{policyCap}},
		{"allow-only policy", allowOnly, domain.AuditCollectHits, []string{policyCap, hitsCap}},
		{"nothing in force", func(*harness) {}, domain.AuditCollectHits, []string{policyCap, hitsCap}},
		{"paused", paused, domain.AuditCollectHits, []string{policyCap, hitsCap}},
	}
	got := map[string]json.RawMessage{}
	for _, sc := range scenarios {
		h := newHarness(t)
		sc.setup(h)
		res := h.compileCollect(base1(), sc.panel, sc.caps...)
		doc, err := json.Marshal(map[string]any{"policy": res.Policy, "digest": res.Digest})
		if err != nil {
			t.Fatal(err)
		}
		got[sc.name] = doc
	}
	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "collect_hits_golden.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wantMap map[string]json.RawMessage
	if err := json.Unmarshal(want, &wantMap); err != nil {
		t.Fatal(err)
	}
	for _, sc := range scenarios {
		var compact bytes.Buffer
		if err := json.Compact(&compact, wantMap[sc.name]); err != nil {
			t.Fatal(err)
		}
		if compact.String() != string(got[sc.name]) {
			t.Errorf("%s: the bytes changed\n got: %s\nwant: %s", sc.name, got[sc.name], compact.String())
		}
	}
}
