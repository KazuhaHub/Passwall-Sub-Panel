package panel

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type registryTestClient struct{ ports.PanelClient }

func TestRegistryDefaultsLegacyRowsAndKeepsKindsDeterministic(t *testing.T) {
	registry := NewRegistry()
	seen := domain.PanelKind("")
	if err := registry.Register(domain.PanelKind3XUI, func(def *domain.Panel) (ports.PanelClient, error) {
		seen = def.Kind
		return &registryTestClient{}, nil
	}); err != nil {
		t.Fatalf("register 3xui: %v", err)
	}
	if err := registry.Register(domain.PanelKindSUI, func(*domain.Panel) (ports.PanelClient, error) {
		return &registryTestClient{}, nil
	}); err != nil {
		t.Fatalf("register sui: %v", err)
	}

	legacy := &domain.Panel{Name: "legacy", URL: "https://example.test"}
	if _, err := registry.NewClient(legacy); err != nil {
		t.Fatalf("new legacy client: %v", err)
	}
	if seen != domain.PanelKind3XUI {
		t.Fatalf("factory kind = %q, want %q", seen, domain.PanelKind3XUI)
	}
	if legacy.Kind != "" {
		t.Fatalf("registry mutated caller definition: kind = %q", legacy.Kind)
	}

	kinds := registry.Kinds()
	if len(kinds) != 2 || kinds[0] != domain.PanelKind3XUI || kinds[1] != domain.PanelKindSUI {
		t.Fatalf("kinds = %#v", kinds)
	}
	if err := registry.Register(domain.PanelKindSUI, func(*domain.Panel) (ports.PanelClient, error) {
		return &registryTestClient{}, nil
	}); err == nil {
		t.Fatal("duplicate adapter registration succeeded")
	}
	if _, err := registry.NewClient(&domain.Panel{Kind: "unknown", Name: "bad"}); err == nil {
		t.Fatal("unknown adapter kind succeeded")
	}
}
