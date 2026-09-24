package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
)

type realityConvergenceNodeRepo struct {
	ports.NodeRepo
	node      *domain.Node
	failWrite bool
	writes    int
}

type realityConvergencePanelRepo struct {
	ports.XUIPanelRepo
	updates int
}

func (r *realityConvergencePanelRepo) UpdateVersion(context.Context, int64, string, string, *time.Time) error {
	r.updates++
	return nil
}

func (r *realityConvergenceNodeRepo) List(context.Context) ([]*domain.Node, error) {
	cp := *r.node
	return []*domain.Node{&cp}, nil
}

func (r *realityConvergenceNodeRepo) GetByID(context.Context, int64) (*domain.Node, error) {
	cp := *r.node
	return &cp, nil
}

func (r *realityConvergenceNodeRepo) UpdateInboundConfig(_ context.Context, next *domain.Node) error {
	r.writes++
	if r.failWrite {
		return errors.New("transient snapshot write failure")
	}
	cp := *next
	r.node = &cp
	return nil
}

func (r *realityConvergenceNodeRepo) CompareAndSwapRealityStream(_ context.Context, panelID, nodeID int64, observed, normalized string) (bool, error) {
	r.writes++
	if r.failWrite {
		return false, errors.New("transient snapshot write failure")
	}
	if r.node.PanelID != panelID || r.node.ID != nodeID || r.node.StreamSettings != observed {
		return false, nil
	}
	r.node.StreamSettings = normalized
	return true, nil
}

func (r *realityConvergenceNodeRepo) ConfirmAppliedConfig(context.Context, int64, int64, domain.NodeConfigIntent) (bool, error) {
	return true, nil
}

type realityConvergenceClient struct{ ports.XUIClient }

func (*realityConvergenceClient) UpdateInbound(context.Context, int, ports.InboundSpec) error {
	return nil
}

type realityConvergencePool struct{ client ports.XUIClient }

func (p realityConvergencePool) Get(int64) (ports.XUIClient, error) { return p.client, nil }
func (realityConvergencePool) List() []*domain.XUIPanel             { return nil }
func (realityConvergencePool) Add(*domain.XUIPanel) error           { return nil }
func (realityConvergencePool) Remove(int64) error                   { return nil }

func newRealityConvergenceApp(repo *realityConvergenceNodeRepo) (*App, *realityConvergencePanelRepo) {
	service := node.New(repo, nil, realityConvergencePool{client: &realityConvergenceClient{}}, nil, nil, nil, nil)
	panels := &realityConvergencePanelRepo{}
	return &App{node: service, repos: ports.Repos{XUIPanel: panels}}, panels
}

func storedFirefoxRealityNode() *domain.Node {
	now := time.Now()
	return &domain.Node{
		ID: 1, PanelID: 7, InboundID: 3, DesiredProtocol: "vless", DesiredPort: 443,
		InboundSettings: `{"decryption":"none"}`,
		StreamSettings:  `{"network":"tcp","security":"reality","realitySettings":{"settings":{"fingerprint":"firefox"}}}`,
		ConfigSyncedAt:  &now,
	}
}

func TestBootObservationConvergesWhenStoredVersionIsAlreadyModern(t *testing.T) {
	repo := &realityConvergenceNodeRepo{node: storedFirefoxRealityNode()}
	a, panels := newRealityConvergenceApp(repo)
	stored := &domain.XUIPanel{ID: 7, XrayVersion: "26.9.9"}
	observed := &ports.ServerStatus{PanelVersion: "3.8.5", XrayVersion: "26.9.9"}

	a.recordProbedPanelVersion(t.Context(), stored, observed, time.Now())
	if panels.updates != 1 || !strings.Contains(repo.node.StreamSettings, `"fingerprint":"chrome"`) {
		t.Fatalf("version writes=%d stream=%s", panels.updates, repo.node.StreamSettings)
	}
}

func TestModernXrayObservationRetriesAFailedConvergence(t *testing.T) {
	repo := &realityConvergenceNodeRepo{node: storedFirefoxRealityNode(), failWrite: true}
	a, panels := newRealityConvergenceApp(repo)
	stored := &domain.XUIPanel{ID: 7, XrayVersion: "26.9.9"}
	observed := &ports.ServerStatus{PanelVersion: "3.8.5", XrayVersion: "26.9.9"}

	a.recordProbedPanelVersion(t.Context(), stored, observed, time.Now())
	repo.failWrite = false
	a.recordProbedPanelVersion(t.Context(), stored, observed, time.Now().Add(time.Second))
	if panels.updates != 2 || repo.writes != 2 || !strings.Contains(repo.node.StreamSettings, `"fingerprint":"chrome"`) {
		t.Fatalf("version writes=%d config writes=%d stream=%s", panels.updates, repo.writes, repo.node.StreamSettings)
	}
}
