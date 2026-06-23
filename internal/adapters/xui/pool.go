package xui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Pool manages one Client per registered 3X-UI panel and routes calls by
// panel id. Construct it once at startup from XUIPanelRepo; service code
// must go through Pool.Get rather than instantiating Clients directly.
type Pool struct {
	mu      sync.RWMutex
	clients map[int64]*Client
	panels  map[int64]*domain.XUIPanel
}

// NewPool builds a Pool from every panel registered in repo.
func NewPool(ctx context.Context, repo ports.XUIPanelRepo) (*Pool, error) {
	panels, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}
	p := &Pool{
		clients: make(map[int64]*Client, len(panels)),
		panels:  make(map[int64]*domain.XUIPanel, len(panels)),
	}
	seenBackends := make(map[string]string, len(panels)) // normalized URL -> first panel name
	for _, panel := range panels {
		c, err := New(panel)
		if err != nil {
			return nil, fmt.Errorf("init xui client %s: %w", panel.Name, err)
		}
		// Surface the duplicate-backend topology that breaks shared-client
		// provisioning: two PSP panels pointing at the SAME 3X-UI server each get
		// their own *Client, the shared client is provisioned through both, and they
		// race on the backend's client_inbounds. The global per-(backend,email) lock
		// (see client.go) now serializes those writes, but the duplicate panel is
		// still wasteful + confusing — warn so the operator can remove it.
		key := strings.TrimRight(panel.URL, "/")
		if first, dup := seenBackends[key]; dup {
			log.Warn("two PSP panels point to the SAME 3X-UI backend URL",
				"url", key, "panel", panel.Name, "duplicate_of", first,
				"hint", "shared-client writes are serialized by URL so this is safe, but you likely want to remove the duplicate panel registration")
		} else {
			seenBackends[key] = panel.Name
		}
		p.clients[panel.ID] = c
		p.panels[panel.ID] = panel
	}
	return p, nil
}

// Get returns the Client registered under panelID.
func (p *Pool) Get(panelID int64) (ports.XUIClient, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.clients[panelID]
	if !ok {
		return nil, fmt.Errorf("xui panel id %d not registered", panelID)
	}
	return c, nil
}

// List returns the registered panels. Order is undefined.
func (p *Pool) List() []*domain.XUIPanel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*domain.XUIPanel, 0, len(p.panels))
	for _, panel := range p.panels {
		cp := *panel
		cp.APIToken = ""
		cp.Password = ""
		out = append(out, &cp)
	}
	return out
}

// Add registers a new 3X-UI client. Returns an error if a panel with the
// same id is already registered.
func (p *Pool) Add(panel *domain.XUIPanel) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.clients[panel.ID]; exists {
		return fmt.Errorf("xui panel id %d already registered", panel.ID)
	}
	c, err := New(panel)
	if err != nil {
		return fmt.Errorf("init xui client %s: %w", panel.Name, err)
	}
	p.clients[panel.ID] = c
	// Store a defensive copy, not the caller's pointer: List() reads p.panels
	// under RLock and copies the struct, which is only safe while the stored
	// value is never mutated. Copying here removes the implicit "never touch
	// this pointer again" contract and prevents a data race if an admin-CRUD
	// path edits the same struct in place after registering it.
	cp := *panel
	p.panels[panel.ID] = &cp
	return nil
}

// Remove unregisters a panel. No-op if the id isn't registered.
func (p *Pool) Remove(panelID int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, panelID)
	delete(p.panels, panelID)
	return nil
}

// Replace atomically swaps the registered client + panel for panel.ID under a
// single write lock. The panel-edit handler used to Remove() then Add() as two
// separate lock cycles, opening a transient window where a concurrent Get()
// (traffic / reconcile / render / sync) saw the id unregistered and failed.
// The new client is built BEFORE the lock (New does no I/O) so a build error
// leaves the pool untouched.
func (p *Pool) Replace(panel *domain.XUIPanel) error {
	c, err := New(panel)
	if err != nil {
		return fmt.Errorf("init xui client %s: %w", panel.Name, err)
	}
	// Defensive copy (same rationale as Add): List() reads p.panels under RLock.
	cp := *panel
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[panel.ID] = c
	p.panels[panel.ID] = &cp
	return nil
}
