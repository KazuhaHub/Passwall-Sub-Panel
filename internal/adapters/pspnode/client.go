// Package pspnode adapts PSP's document/report native-node protocol to the
// existing imperative PanelClient port. Writes intentionally record no second
// state here: PSP's repositories already hold intent and nodesync publishes it.
// Reads are projected only from a full agent report that observed runtime.
package pspnode

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/KazuhaHub/passwall-node/corecatalog"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Client struct {
	panelID int64
	reader  ports.NativePanelSnapshotReader
	nodes   ports.NodeRepo
	agents  ports.NodeAgentRepo

	allocationMu sync.Mutex
	nextInbound  int
}

func New(panel *domain.Panel, reader ports.NativePanelSnapshotReader, nodes ports.NodeRepo, agents ports.NodeAgentRepo) (*Client, error) {
	if panel == nil || panel.ID == 0 {
		return nil, errors.New("PSP native panel definition with a persisted ID is required")
	}
	if reader == nil || nodes == nil || agents == nil {
		return nil, errors.New("PSP native snapshot reader, node repository and agent repository are required")
	}
	return &Client{panelID: panel.ID, reader: reader, nodes: nodes, agents: agents}, nil
}

func (c *Client) Capabilities() []ports.PanelCapability {
	return []ports.PanelCapability{
		ports.CapabilityInboundRead,
		ports.CapabilityInboundWrite,
		ports.CapabilityInboundCreate,
		ports.CapabilityInboundUpdate,
		ports.CapabilityInboundDelete,
		ports.CapabilityInboundEnable,
		ports.CapabilityClientRead,
		ports.CapabilityClientWrite,
		ports.CapabilityTrafficRead,
		ports.CapabilityStatusRead,
		ports.CapabilityCoreUpgrade,
		// CapabilityClientIPLimit is absent until B3 turns v1 shadow
		// observation into an enforcement decision backed by evidence.
	}
}

func (c *Client) GetCoreVersionList(ctx context.Context) ([]string, error) {
	return c.GetCoreVersionListForEngine(ctx, domain.NodeCoreXray)
}

func (c *Client) GetCoreVersionListForEngine(_ context.Context, engine domain.NodeCoreEngine) ([]string, error) {
	engine = domain.NormalizeNodeCoreEngine(engine)
	if !engine.Valid() {
		return nil, errors.New("unsupported native core engine")
	}
	releases, err := corecatalog.List(string(engine))
	if err != nil {
		return nil, err
	}
	versions := make([]string, len(releases))
	for index, release := range releases {
		versions[index] = release.Version
	}
	return versions, nil
}

func (c *Client) InstallCore(ctx context.Context, version string) error {
	release, err := corecatalog.Resolve(string(domain.NodeCoreXray), version)
	if err != nil {
		return err
	}
	return c.InstallCoreEngine(ctx, domain.NodeCoreXray, release.Version, release.RequiresConfirmation)
}

func (c *Client) InstallCoreEngine(ctx context.Context, engine domain.NodeCoreEngine, version string, allowRestrictedReality bool) error {
	engine = domain.NormalizeNodeCoreEngine(engine)
	if !engine.Valid() {
		return errors.New("unsupported native core engine")
	}
	release, err := corecatalog.Resolve(string(engine), version)
	if err != nil {
		return err
	}
	if release.RequiresConfirmation != allowRestrictedReality {
		return errors.New("native core restriction acknowledgement mismatch")
	}
	agent, err := c.agents.GetByPanelID(ctx, c.panelID)
	if err != nil {
		return err
	}
	return c.agents.UpdateCoreSelection(ctx, agent.AgentID, engine, release.Version, allowRestrictedReality)
}

func (c *Client) ApplyIsAsynchronous() bool { return true }

func (c *Client) snapshot(ctx context.Context) (*ports.NativePanelSnapshot, error) {
	snapshot, err := c.reader.NativePanelSnapshot(ctx, c.panelID)
	if errors.Is(err, domain.ErrNotFound) {
		return &ports.NativePanelSnapshot{
			Clients:       make(map[string]ports.ClientDetail),
			LiveClientIPs: make(map[string][]string),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (c *Client) ListInbounds(ctx context.Context) ([]ports.Inbound, error) {
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return cloneInbounds(snapshot.Inbounds), nil
}

func (c *Client) ListInboundsSlim(ctx context.Context) ([]ports.Inbound, error) {
	return c.ListInbounds(ctx)
}

func (c *Client) GetInbound(ctx context.Context, id int) (*ports.Inbound, error) {
	inbounds, err := c.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	for i := range inbounds {
		if inbounds[i].ID == id {
			return &inbounds[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

// AddInbound returns a compatibility-only numeric inbound id. The native wire
// key remains nodes.id; this allocation exists solely because the legacy node
// service asks PanelClient for an id before it inserts that stable row.
func (c *Client) AddInbound(ctx context.Context, _ ports.InboundSpec) (int, error) {
	c.allocationMu.Lock()
	defer c.allocationMu.Unlock()
	if c.nextInbound == 0 {
		nodes, err := c.nodes.List(ctx)
		if err != nil {
			return 0, err
		}
		for _, node := range nodes {
			if node != nil && node.PanelID == c.panelID && node.InboundID >= c.nextInbound {
				c.nextInbound = node.InboundID + 1
			}
		}
		if c.nextInbound == 0 {
			c.nextInbound = 1
		}
	}
	id := c.nextInbound
	c.nextInbound++
	return id, nil
}

func (c *Client) UpdateInbound(context.Context, int, ports.InboundSpec) error { return nil }
func (c *Client) DelInbound(context.Context, int) error                       { return nil }
func (c *Client) SetInboundEnable(context.Context, int, bool) error           { return nil }

func (c *Client) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	return c.AddClientToInbounds(ctx, []int{inboundID}, spec)
}

func (c *Client) UpdateClient(context.Context, ports.ClientSpec) error { return nil }
func (c *Client) DelClientByEmail(context.Context, string) error       { return nil }
func (c *Client) AddClientToInbounds(context.Context, []int, ports.ClientSpec) error {
	return nil
}
func (c *Client) AttachClient(context.Context, string, []int) error { return nil }
func (c *Client) DetachClient(context.Context, string, []int) error { return nil }

func (c *Client) GetClient(ctx context.Context, email string) (*ports.ClientDetail, error) {
	if email == "" {
		return nil, errors.New("GetClient: email is required")
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	detail, ok := snapshot.Clients[email]
	if !ok {
		return nil, nil
	}
	copy := cloneClientDetail(detail)
	return &copy, nil
}

func (c *Client) ListClientInbounds(ctx context.Context) (map[string][]int, error) {
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]int, len(snapshot.Clients))
	for email, detail := range snapshot.Clients {
		result[email] = append([]int(nil), detail.InboundIDs...)
	}
	return result, nil
}

func (c *Client) BulkDelByEmail(_ context.Context, emails []string) (int, error) {
	return len(emails), nil
}

func (c *Client) BulkAttach(_ context.Context, emails []string, _ []int) (ports.BulkAttachResult, error) {
	return ports.BulkAttachResult{Done: append([]string(nil), emails...)}, nil
}

func (c *Client) BulkCreateClients(_ context.Context, items []ports.BulkCreateClientItem) (ports.BulkCreateResult, error) {
	return ports.BulkCreateResult{Created: len(items)}, nil
}

func (c *Client) GetServerStatus(ctx context.Context) (*ports.ServerStatus, error) {
	// Unlike list/read compatibility calls, status must not convert "no full
	// report yet" into an empty successful snapshot. The admin connection test
	// and health indicator need offline to remain distinguishable from a live
	// zero-inbound node.
	snapshot, err := c.reader.NativePanelSnapshot(ctx, c.panelID)
	if err != nil {
		return nil, err
	}
	status := snapshot.Status
	return &status, nil
}

func (c *Client) ListLiveClientIPs(ctx context.Context) (map[string][]string, error) {
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(snapshot.LiveClientIPs))
	for email, ips := range snapshot.LiveClientIPs {
		result[email] = append([]string(nil), ips...)
	}
	return result, nil
}

func cloneInbounds(in []ports.Inbound) []ports.Inbound {
	out := append([]ports.Inbound(nil), in...)
	for i := range out {
		out[i].ClientStats = append([]ports.ClientTraffic(nil), in[i].ClientStats...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func cloneClientDetail(in ports.ClientDetail) ports.ClientDetail {
	in.InboundIDs = append([]int(nil), in.InboundIDs...)
	return in
}

var (
	_ ports.PanelClient         = (*Client)(nil)
	_ ports.CapabilityProvider  = (*Client)(nil)
	_ ports.LiveIPReader        = (*Client)(nil)
	_ ports.AsynchronousApplier = (*Client)(nil)
	_ ports.CoreUpdater         = (*Client)(nil)
)
