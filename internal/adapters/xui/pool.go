package xui

import (
	"context"
	"fmt"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Pool manages one Client per registered 3X-UI panel and routes calls by
// panel name. Construct it once at startup from XUIPanelRepo; service code
// must go through Pool.Get rather than instantiating Clients directly.
type Pool struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewPool builds a Pool from every panel registered in repo.
func NewPool(ctx context.Context, repo ports.XUIPanelRepo) (*Pool, error) {
	panels, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}
	p := &Pool{clients: make(map[string]*Client, len(panels))}
	for _, panel := range panels {
		c, err := New(panel)
		if err != nil {
			return nil, fmt.Errorf("init xui client %s: %w", panel.Name, err)
		}
		p.clients[panel.Name] = c
	}
	return p, nil
}

// Get returns the Client registered under panelName.
func (p *Pool) Get(panelName string) (ports.XUIClient, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.clients[panelName]
	if !ok {
		return nil, fmt.Errorf("xui panel %q not registered", panelName)
	}
	return c, nil
}

// List returns the registered panel names. Order is undefined.
func (p *Pool) List() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.clients))
	for name := range p.clients {
		out = append(out, name)
	}
	return out
}
