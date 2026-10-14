package sharedclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type provisionSettings struct {
	ports.ScopedSettings
	width int
}

func (s provisionSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{MaxPanelConcurrency: s.width}, nil
}

type provisionClients struct {
	*fakeClients
	attachmentsByClient map[int64][]domain.PSPClientInbound
}

func (r *provisionClients) ListInbounds(_ context.Context, id int64) ([]domain.PSPClientInbound, error) {
	return r.attachmentsByClient[id], nil
}

type provisionPool struct {
	ports.XUIPool
	panels map[int64]*provisionPanel
}

func (p provisionPool) Get(id int64) (ports.XUIClient, error) { return p.panels[id], nil }

type provisionMonitor struct {
	mu               sync.Mutex
	active, peak     int
	byPanel          map[int64]int
	samePanelOverlap bool
	firstByPanel     map[int64][]string
	arrived          chan int64
}

type provisionPanel struct {
	ports.XUIClient
	mu            sync.Mutex
	id            int64
	async         bool
	monitor       *provisionMonitor
	release       chan struct{}
	seen, created map[string]bool
	inbounds      map[string][]int
	fail          map[string]error
	panicEmail    string
	adds          int
}

func (p *provisionPanel) ApplyIsAsynchronous() bool { return p.async }
func (p *provisionPanel) GetClient(ctx context.Context, email string) (*ports.ClientDetail, error) {
	p.mu.Lock()
	first := !p.seen[email]
	p.seen[email] = true
	p.mu.Unlock()
	m := p.monitor
	m.mu.Lock()
	m.active++
	if m.active > m.peak {
		m.peak = m.active
	}
	m.byPanel[p.id]++
	m.samePanelOverlap = m.samePanelOverlap || m.byPanel[p.id] > 1
	if first {
		m.firstByPanel[p.id] = append(m.firstByPanel[p.id], email)
	}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.active--; m.byPanel[p.id]--; m.mu.Unlock() }()
	if first {
		m.arrived <- p.id
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if email == p.panicEmail {
		panic("private-panel-panic-marker")
	}
	p.mu.Lock()
	created := p.created[email]
	p.mu.Unlock()
	if !created {
		return nil, nil
	}
	return &ports.ClientDetail{Email: email, ID: "observed-fixture", InboundIDs: p.inbounds[email]}, nil
}
func (p *provisionPanel) AddClientToInbounds(ctx context.Context, _ []int, spec ports.ClientSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.adds++
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.fail[spec.Email]; err != nil {
		return err
	}
	p.created[spec.Email] = true
	return nil
}

func provisionFanoutFixture(async bool, width int) (*Service, *provisionClients, provisionPool, *provisionMonitor) {
	rows := []*domain.PSPClient{
		{ID: 1, UserID: 7, PanelID: 10, Email: "first@fixture.test"},
		{ID: 2, UserID: 7, PanelID: 20, Email: "second@fixture.test"},
		{ID: 3, UserID: 7, PanelID: 10, Email: "third@fixture.test"},
		{ID: 4, UserID: 7, PanelID: 30, Email: "fourth@fixture.test"},
	}
	r := &provisionClients{fakeClients: &fakeClients{byUser: rows}, attachmentsByClient: make(map[int64][]domain.PSPClientInbound)}
	m := &provisionMonitor{byPanel: make(map[int64]int), firstByPanel: make(map[int64][]string), arrived: make(chan int64, 10)}
	p := provisionPool{panels: make(map[int64]*provisionPanel)}
	nodes := fakeNodes{byID: make(map[int64]*domain.Node)}
	for _, c := range rows {
		r.attachmentsByClient[c.ID] = []domain.PSPClientInbound{{ClientID: c.ID, NodeID: c.ID}}
		nodes.byID[c.ID] = enabledNode(c.ID, c.PanelID, int(c.ID)+100)
		if p.panels[c.PanelID] == nil {
			p.panels[c.PanelID] = &provisionPanel{id: c.PanelID, async: async, monitor: m, release: make(chan struct{}), seen: make(map[string]bool), created: make(map[string]bool), inbounds: make(map[string][]int), fail: make(map[string]error)}
		}
		p.panels[c.PanelID].inbounds[c.Email] = []int{int(c.ID) + 100}
	}
	r.attachmentsByClient[4] = append(r.attachmentsByClient[4], domain.PSPClientInbound{ClientID: 4, NodeID: 999})
	return New(r, p, nodes).WithSettings(provisionSettings{width: width}), r, p, m
}

func TestProvisionUserBoundsPanelsPreservesOrderAndWaitsForAllResults(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%v", async), func(t *testing.T) {
			svc, clients, pool, m := provisionFanoutFixture(async, 2)
			firstErr, secondErr := errors.New("first input failure"), errors.New("earlier completion failure")
			pool.panels[10].fail[clients.byUser[0].Email] = firstErr
			pool.panels[20].fail[clients.byUser[1].Email] = secondErr
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			type outcome struct {
				result ProvisionResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() { result, err := svc.ProvisionUser(ctx, 7); done <- outcome{result, err} }()
			initial := make(map[int64]bool)
			for range 2 {
				select {
				case id := <-m.arrived:
					initial[id] = true
				case <-ctx.Done():
					t.Fatal("different panels remained serial")
				}
			}
			if !initial[10] || !initial[20] {
				t.Fatal("fan-out exceeded its width or reordered same-panel clients")
			}
			close(pool.panels[20].release)
			select {
			case id := <-m.arrived:
				if id != 30 {
					t.Fatal("blocked panel admitted its next client")
				}
			case <-ctx.Done():
				t.Fatal("slow panel prevented independent panel progress")
			}
			close(pool.panels[30].release)
			select {
			case <-done:
				t.Fatal("provision returned before all panels completed")
			default:
			}
			close(pool.panels[10].release)
			var got outcome
			select {
			case got = <-done:
			case <-ctx.Done():
				t.Fatal("provision failed to join all workers")
			}
			if !errors.Is(got.err, firstErr) || got.result != (ProvisionResult{Created: true, Provisioned: 2, Skipped: 1}) {
				t.Fatal("completion order changed first error or aggregate results")
			}
			if m.peak != 2 || m.active != 0 || m.samePanelOverlap || strings.Join(m.firstByPanel[10], ",") != "first@fixture.test,third@fixture.test" {
				t.Fatal("configured bound or same-panel ordering was lost")
			}
			for _, panel := range pool.panels {
				if panel.adds != len(panel.inbounds) {
					t.Fatal("an error omitted a later client")
				}
			}
			if async && len(clients.applied) != 0 || !async && len(clients.applied) != 2 {
				t.Fatal("native asynchronous confirmation authority changed")
			}
		})
	}
}

func TestProvisionUserPanicAndCancellationCannotBecomeSuccessfulProvision(t *testing.T) {
	for _, failure := range []string{"panic", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			svc, clients, pool, monitor := provisionFanoutFixture(true, 1)
			for _, panel := range pool.panels {
				close(panel.release)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if failure == "panic" {
				pool.panels[10].panicEmail = clients.byUser[0].Email
			} else {
				cancel()
			}
			got, err := svc.ProvisionUser(ctx, 7)
			if err == nil || strings.Contains(err.Error(), "private-panel-panic-marker") {
				t.Fatal("panic or cancellation became success or leaked private panic text")
			}
			if failure == "panic" && (got.Provisioned != 3 || !got.Created || len(pool.panels[10].seen) != 2) {
				t.Fatal("panic prevented later clients from being attempted")
			}
			if failure == "cancel" && (!errors.Is(err, context.Canceled) || got.Provisioned != 0 || got.Created) {
				t.Fatal("cancelled worker fabricated provision")
			}
			if len(clients.applied) != 0 {
				t.Fatal("async workers wrote legacy applied state")
			}
			if monitor.peak != 1 || monitor.active != 0 || monitor.samePanelOverlap {
				t.Fatal("configured single-worker provision bound was lost")
			}
		})
	}
}
