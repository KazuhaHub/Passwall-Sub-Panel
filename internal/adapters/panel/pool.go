package panel

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Pool holds clients from multiple adapter implementations and routes by the
// stable local panel ID.
type Pool struct {
	mu       sync.RWMutex
	registry *Registry
	clients  map[int64]ports.PanelClient
	panels   map[int64]*domain.Panel
}

func NewPool(ctx context.Context, repo ports.XUIPanelRepo, registry *Registry) (*Pool, error) {
	if registry == nil {
		return nil, fmt.Errorf("panel adapter registry is nil")
	}
	defs, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}
	p := &Pool{
		registry: registry,
		clients:  make(map[int64]ports.PanelClient, len(defs)),
		panels:   make(map[int64]*domain.Panel, len(defs)),
	}
	seen := make(map[string]string, len(defs))
	for _, def := range defs {
		client, err := registry.NewClient(def)
		if err != nil {
			return nil, err
		}
		key := string(domain.NormalizePanelKind(def.Kind)) + "\x00" + strings.TrimRight(def.URL, "/")
		if first, duplicate := seen[key]; duplicate {
			log.Warn("two panel registrations point to the same backend",
				"kind", domain.NormalizePanelKind(def.Kind), "url", def.URL,
				"panel", def.Name, "duplicate_of", first)
		} else {
			seen[key] = def.Name
		}
		copy := *def
		copy.Kind = domain.NormalizePanelKind(copy.Kind)
		p.clients[copy.ID] = client
		p.panels[copy.ID] = &copy
	}
	return p, nil
}

func (p *Pool) Get(panelID int64) (ports.PanelClient, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	client, ok := p.clients[panelID]
	if !ok {
		return nil, fmt.Errorf("panel id %d not registered", panelID)
	}
	return client, nil
}

func (p *Pool) List() []*domain.Panel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*domain.Panel, 0, len(p.panels))
	for _, def := range p.panels {
		copy := *def
		copy.APIToken = ""
		copy.Password = ""
		out = append(out, &copy)
	}
	return out
}

func (p *Pool) Add(def *domain.Panel) error {
	client, err := p.registry.NewClient(def)
	if err != nil {
		return err
	}
	copy := *def
	copy.Kind = domain.NormalizePanelKind(copy.Kind)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.clients[def.ID]; exists {
		return fmt.Errorf("panel id %d already registered", def.ID)
	}
	p.clients[def.ID] = client
	p.panels[def.ID] = &copy
	return nil
}

func (p *Pool) Remove(panelID int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, panelID)
	delete(p.panels, panelID)
	return nil
}

func (p *Pool) SupportsKind(kind domain.PanelKind) bool {
	return p.registry.Has(kind)
}

// Replace atomically swaps the adapter and definition after constructing the
// new client successfully. Changing Kind at runtime is therefore safe.
func (p *Pool) Replace(def *domain.Panel) error {
	client, err := p.registry.NewClient(def)
	if err != nil {
		return err
	}
	copy := *def
	copy.Kind = domain.NormalizePanelKind(copy.Kind)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[def.ID] = client
	p.panels[def.ID] = &copy
	return nil
}

var _ ports.PanelPool = (*Pool)(nil)
