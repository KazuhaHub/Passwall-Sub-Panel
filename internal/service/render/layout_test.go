package render

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestApplyLayout_SeparatorNodeEmitsSeparatorItem documents that a node
// whose Kind is "separator" must collapse into a renderItem with
// isSeparator=true so buildProxies routes it through emitSeparator
// (DIRECT proxy named display_name) instead of trying to fetch a 3X-UI
// inbound that doesn't exist for that row.
func TestApplyLayout_SeparatorNodeEmitsSeparatorItem(t *testing.T) {
	real := &domain.Node{ID: 1, DisplayName: "TW Static", Kind: domain.NodeKindReal, SortOrder: 10, Region: "TW"}
	sep := &domain.Node{ID: 2, DisplayName: "---- Taiwan HiNet ----", Kind: domain.NodeKindSeparator, SortOrder: 5, Region: "TW"}
	real2 := &domain.Node{ID: 3, DisplayName: "TW Dynamic", Kind: domain.NodeKindReal, SortOrder: 20, Region: "TW"}

	items := applyLayout([]*domain.Node{real, sep, real2}, domain.Layout{})
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	// sort_order 5 < 10 < 20, so separator should be first
	if !items[0].isSeparator {
		t.Errorf("items[0].isSeparator = false, want true (separator with smallest sort_order)")
	}
	if items[0].name != "---- Taiwan HiNet ----" {
		t.Errorf("items[0].name = %q, want display_name verbatim", items[0].name)
	}
	if items[0].node != nil {
		t.Errorf("items[0].node should be nil for separator entries (got %+v)", items[0].node)
	}
	// Real nodes come after, keep their node ptr
	if items[1].isSeparator || items[1].node == nil || items[1].node.ID != 1 {
		t.Errorf("items[1] should wrap the real node id=1, got %+v", items[1])
	}
	if items[2].isSeparator || items[2].node == nil || items[2].node.ID != 3 {
		t.Errorf("items[2] should wrap the real node id=3, got %+v", items[2])
	}
}

// TestApplyLayout_LegacyEmptyKindStillReal: rows written before the
// Kind column existed have Kind == "" — they must keep emitting as
// real nodes (no silent reclassification to separator).
func TestApplyLayout_LegacyEmptyKindStillReal(t *testing.T) {
	n := &domain.Node{ID: 42, DisplayName: "legacy", Kind: "", SortOrder: 10}
	items := applyLayout([]*domain.Node{n}, domain.Layout{})
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].isSeparator {
		t.Errorf("legacy empty-Kind node should not render as separator")
	}
	if items[0].node == nil || items[0].node.ID != 42 {
		t.Errorf("legacy node should still wrap as real node, got %+v", items[0])
	}
}
