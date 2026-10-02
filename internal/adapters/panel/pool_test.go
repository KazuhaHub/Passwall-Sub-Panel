package panel

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func newKindTestPool(t *testing.T) *Pool {
	t.Helper()
	registry := NewRegistry()
	for _, kind := range []domain.PanelKind{domain.PanelKind3XUI, domain.PanelKindSUI, domain.PanelKindPSP} {
		if err := registry.Register(kind, func(*domain.Panel) (ports.PanelClient, error) {
			return &registryTestClient{}, nil
		}); err != nil {
			t.Fatalf("register %s: %v", kind, err)
		}
	}
	return &Pool{registry: registry, clients: map[int64]ports.PanelClient{}, panels: map[int64]*domain.Panel{}}
}

// The lifecycle failure breakdown labels each failure by the kind of panel it
// happened on, and the pool is the one place that knows the kind behind an id.
// It answers with the NORMALISED kind, so a legacy row with no kind reads as
// 3X-UI here exactly as it does everywhere else.
func TestPoolKindOfReportsTheNormalisedKind(t *testing.T) {
	p := newKindTestPool(t)
	for _, def := range []*domain.Panel{
		{ID: 1, Kind: "", Name: "legacy"},
		{ID: 2, Kind: domain.PanelKindSUI, Name: "s-ui"},
		{ID: 3, Kind: domain.PanelKindPSP, Name: "native"},
	} {
		if err := p.Add(def); err != nil {
			t.Fatalf("add %s: %v", def.Name, err)
		}
	}
	for id, want := range map[int64]domain.PanelKind{1: domain.PanelKind3XUI, 2: domain.PanelKindSUI, 3: domain.PanelKindPSP} {
		got, ok := p.KindOf(id)
		if !ok || got != want {
			t.Errorf("KindOf(%d) = %q, %v; want %q, true", id, got, ok, want)
		}
	}
}

// An id the pool does not hold is exactly the case Get fails on, so KindOf
// must not invent a kind for it.
func TestPoolKindOfSaysNothingForAnUnregisteredPanel(t *testing.T) {
	p := newKindTestPool(t)
	if err := p.Add(&domain.Panel{ID: 2, Kind: domain.PanelKindSUI, Name: "s-ui"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.KindOf(99); ok {
		t.Fatal("KindOf reported a kind for an id the pool never held")
	}
	if err := p.Remove(2); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.KindOf(2); ok {
		t.Fatal("KindOf still reports a kind after the panel was removed")
	}
}
