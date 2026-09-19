package panel

import (
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// registryTestClient is a registry double. It states its capabilities because
// the registry refuses to build a client that does not — see the test below.
// Local doubles elsewhere in the codebase are unaffected: they are handed
// straight to services and never pass through here.
type registryTestClient struct{ ports.PanelClient }

func (c *registryTestClient) Capabilities() []ports.PanelCapability { return nil }

// registrySilentClient embeds the interface and nothing else, so it satisfies
// ports.PanelClient while saying nothing about what it can do. That is the shape
// ports.SupportsCapability answers "yes" for, which is reasonable for a
// hand-made double and unacceptable for an adapter the panel will talk to: every
// capability-gated button in the UI would appear, and the action behind it would
// come back 501.
type registrySilentClient struct{ ports.PanelClient }

// An adapter that cannot state its capabilities must not be constructible
// through the registry, which is the only path production takes.
func TestNewClientRejectsAdapterThatCannotStateItsCapabilities(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(domain.PanelKind3XUI, func(*domain.Panel) (ports.PanelClient, error) {
		return &registrySilentClient{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := registry.NewClient(&domain.Panel{Kind: domain.PanelKind3XUI, Name: "silent"})
	if err == nil {
		t.Fatal("an adapter that cannot state its capabilities was constructed; SupportsCapability would then report every capability as present")
	}
	// The error has to name the adapter, or a developer meeting it at startup
	// has to guess which of the registered kinds is the silent one.
	if !strings.Contains(err.Error(), string(domain.PanelKind3XUI)) {
		t.Fatalf("the error must name the adapter, got %v", err)
	}
}

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
