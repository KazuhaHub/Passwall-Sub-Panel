// Package panel provides the adapter registry and the heterogeneous client
// pool. It depends only on domain/ports so concrete adapters can register
// themselves at composition-root startup without import cycles.
package panel

import (
	"fmt"
	"sort"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Factory constructs one client for a persisted panel definition. It must not
// perform network I/O; connectivity is verified through the normal probe path.
type Factory func(*domain.Panel) (ports.PanelClient, error)

// Registry maps stable panel kinds to adapter factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[domain.PanelKind]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[domain.PanelKind]Factory)}
}

// Register installs one adapter. Duplicate registration is rejected so plugin
// load order cannot silently replace a backend implementation.
func (r *Registry) Register(kind domain.PanelKind, factory Factory) error {
	kind = domain.NormalizePanelKind(kind)
	if kind == "" {
		return fmt.Errorf("panel adapter kind is required")
	}
	if factory == nil {
		return fmt.Errorf("panel adapter %q has nil factory", kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[kind]; exists {
		return fmt.Errorf("panel adapter %q already registered", kind)
	}
	r.factories[kind] = factory
	return nil
}

// NewClient resolves a panel's kind and builds its adapter client.
func (r *Registry) NewClient(def *domain.Panel) (ports.PanelClient, error) {
	if def == nil {
		return nil, fmt.Errorf("panel definition is nil")
	}
	kind := domain.NormalizePanelKind(def.Kind)
	r.mu.RLock()
	factory := r.factories[kind]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("panel adapter %q is not registered", kind)
	}
	copy := *def
	copy.Kind = kind
	client, err := factory(&copy)
	if err != nil {
		return nil, fmt.Errorf("init %s adapter for %s: %w", kind, def.Name, err)
	}
	// AN ADAPTER MUST BE ABLE TO SAY WHAT IT CAN DO. ports.SupportsCapability
	// answers "yes" for anything that does not implement CapabilityProvider,
	// which is deliberate for hand-made test doubles but catastrophic for a
	// production adapter: every capability-gated action would be offered, and
	// each one would come back 501 from the panel it was offered for. The
	// registry is the only path production takes, so the requirement is enforced
	// here rather than left to whoever adds the next adapter to remember.
	if _, ok := client.(ports.CapabilityProvider); !ok {
		return nil, fmt.Errorf("panel adapter %q cannot report its capabilities: a production adapter must implement ports.CapabilityProvider, because SupportsCapability otherwise reports every capability as present", kind)
	}
	return client, nil
}

func (r *Registry) Kinds() []domain.PanelKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.PanelKind, 0, len(r.factories))
	for kind := range r.factories {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (r *Registry) Has(kind domain.PanelKind) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[domain.NormalizePanelKind(kind)]
	return ok
}
