package node

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// opsNativeClient stands in for pspnode.Client: a native panel applies
// asynchronously, GetInbound answers from the agent's last full report, and
// AddInbound only hands out a compatibility id.
type opsNativeClient struct {
	ports.XUIClient
	getErr  error
	added   int
	updated int
	deleted int
}

func (*opsNativeClient) ApplyIsAsynchronous() bool { return true }
func (c *opsNativeClient) GetInbound(context.Context, int) (*ports.Inbound, error) {
	return nil, c.getErr
}
func (c *opsNativeClient) AddInbound(context.Context, ports.InboundSpec) (int, error) {
	c.added++
	return 99, nil
}
func (c *opsNativeClient) UpdateInbound(context.Context, int, ports.InboundSpec) error {
	c.updated++
	return nil
}
func (c *opsNativeClient) DelInbound(context.Context, int) error { c.deleted++; return nil }

// opsXUIClient stands in for a 3X-UI client whose inbound lookup fails.
type opsXUIClient struct {
	ports.XUIClient
	getErr error
}

func (c opsXUIClient) GetInbound(context.Context, int) (*ports.Inbound, error) {
	return nil, c.getErr
}

// opsCleaner records which cleanup steps the node-delete task ran.
type opsCleaner struct {
	InboundCleaner
	unclaimed, ensured, delOwned, deleted int
}

func (c *opsCleaner) UnclaimAllForInbound(context.Context, int64, int) error {
	c.unclaimed++
	return nil
}
func (c *opsCleaner) EnsureInboundDeletable(context.Context, int64, int) error {
	c.ensured++
	return nil
}
func (c *opsCleaner) DelAllOwnedForInbound(context.Context, int64, int) error {
	c.delOwned++
	return nil
}
func (c *opsCleaner) DeleteInbound(context.Context, int64, int) error { c.deleted++; return nil }

func opsDeleteFixture(client ports.XUIClient) (*Service, *detachNodeRepo, *opsCleaner) {
	repo := &detachNodeRepo{captureNodeRepo: &captureNodeRepo{node: &domain.Node{
		ID: 7, PanelID: 10, InboundID: 5, Enabled: false, DesiredProtocol: "vless", DesiredPort: 443,
	}}}
	cleaner := &opsCleaner{}
	return &Service{nodes: repo, pool: stubXUIPool{c: client}, cleaner: cleaner}, repo, cleaner
}

func runNodeDelete(svc *Service) error {
	return svc.runNodeTask(context.Background(), &domain.SyncTask{Type: domain.SyncTaskNodeDelete, TargetID: 7})
}

// Deleting a native node means removing it from PSP's desired config; the agent
// drops it on its next sync and no upstream call is needed. So the task must not
// gate on the agent's report: a node the agent never reported would otherwise
// retry to the attempt cap and leave the row disabled.
func TestNativeNodeDeleteDoesNotDependOnTheAgentReport(t *testing.T) {
	for name, getErr := range map[string]error{
		"before the agent's first full report": ports.ErrNativePanelSnapshotMissing,
		"agent reporting, node not in it yet":  domain.ErrNotFound,
		"agent offline":                        ports.ErrNativePanelAgentOffline,
		"report stale":                         ports.ErrNativePanelSnapshotStale,
	} {
		t.Run(name, func(t *testing.T) {
			svc, repo, cleaner := opsDeleteFixture(&opsNativeClient{getErr: getErr})
			if err := runNodeDelete(svc); err != nil {
				t.Fatalf("delete task err = %v, want success", err)
			}
			if !repo.deleted {
				t.Fatal("node row was not removed")
			}
			if cleaner.unclaimed != 1 {
				t.Fatalf("ownership rows must be dropped locally, unclaimed=%d", cleaner.unclaimed)
			}
		})
	}
}

// The synchronous 3X-UI path is unchanged: the same "not found" shapes the
// native panel produces are NOT treated as "inbound gone" there, because a
// bare "not found" (reverse proxy, localized 404 page) must never make the task
// drop the row while the live inbound still exists.
func TestXUINodeDeleteKeepsItsCarefulNotFoundMatching(t *testing.T) {
	for name, getErr := range map[string]error{
		"domain not found":             domain.ErrNotFound,
		"snapshot-missing shaped":      ports.ErrNativePanelSnapshotMissing,
		"bare reverse-proxy not found": errors.New("Not Found"),
		"panel unreachable":            errors.New("dial tcp: connection refused"),
	} {
		t.Run("keeps retrying on "+name, func(t *testing.T) {
			svc, repo, cleaner := opsDeleteFixture(opsXUIClient{getErr: getErr})
			if err := runNodeDelete(svc); err == nil {
				t.Fatal("task must fail (and retry), not succeed")
			}
			if repo.deleted || cleaner.unclaimed != 0 {
				t.Fatalf("row dropped on an ambiguous error: deleted=%v unclaimed=%d", repo.deleted, cleaner.unclaimed)
			}
		})
	}
	for name, getErr := range map[string]error{
		"record not found":     errors.New("record not found"),
		"not found in inbound": errors.New("client not found in inbound"),
		"http 404":             errors.New("GET /panel/api/inbounds/get/5: http 404"),
	} {
		t.Run("drops the row on "+name, func(t *testing.T) {
			svc, repo, cleaner := opsDeleteFixture(opsXUIClient{getErr: getErr})
			if err := runNodeDelete(svc); err != nil {
				t.Fatalf("err = %v", err)
			}
			if !repo.deleted || cleaner.unclaimed != 1 {
				t.Fatalf("deleted=%v unclaimed=%d", repo.deleted, cleaner.unclaimed)
			}
		})
	}
}

// ---- Recreate ----

type opsRecreateRepo struct {
	recreateNodeRepo
}

func opsRecreateFixture(client ports.XUIClient) (*Service, *opsRecreateRepo, *recreateResyncer, *int) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 10, InboundID: 5, Enabled: true,
		DesiredProtocol: "vless", DesiredPort: 443,
		InboundSettings: `{"clients":[]}`,
		StreamSettings:  `{"network":"tcp","security":"reality"}`,
		ConfigSyncedAt:  &now, ConfigSyncState: domain.ConfigSyncSynced, // a captured config is present
	}
	repo := &opsRecreateRepo{recreateNodeRepo{node: node}}
	resync := &recreateResyncer{}
	svc := &Service{
		nodes: repo, pool: recreatePool{c: client}, resyncer: resync,
		groups: recreateGroupsList{groups: []*domain.Group{{ID: 7, TagFilter: domain.TagFilter{All: true}}}},
		users:  recreateUsers{byGroup: map[int64][]*domain.User{7: {{ID: 100, Enabled: true}}}},
	}
	svc.SetBackgroundRunner(func(_ string, fn func(context.Context)) { fn(context.Background()) })
	routeCalls := new(int)
	svc.SetRouteCleaner(func(context.Context, int64) error { *routeCalls++; return nil })
	return svc, repo, resync, routeCalls
}

// On a native panel PSP owns the inbound and the agent deploys whatever PSP's
// desired config says, so a report that lacks the node (not reported yet, agent
// offline, stale) says nothing about the inbound being gone. Recreate must not
// clear the node's routes, allocate a new compatibility id, or relink the node;
// it re-provisions the members, which is the part that still means something.
func TestNativeRecreateDoesNotTreatAMissingReportAsAMissingInbound(t *testing.T) {
	for name, getErr := range map[string]error{
		"before the agent's first full report": ports.ErrNativePanelSnapshotMissing,
		"agent reporting, node not in it yet":  domain.ErrNotFound,
		"agent offline":                        ports.ErrNativePanelAgentOffline,
		"report stale":                         ports.ErrNativePanelSnapshotStale,
	} {
		t.Run(name, func(t *testing.T) {
			client := &opsNativeClient{getErr: getErr}
			svc, repo, resync, routeCalls := opsRecreateFixture(client)
			if err := svc.RecreateInboundOnServer(context.Background(), 1); err != nil {
				t.Fatalf("err = %v", err)
			}
			if *routeCalls != 0 {
				t.Fatalf("routes were cleared %d time(s)", *routeCalls)
			}
			if client.added != 0 || client.deleted != 0 {
				t.Fatalf("inbound id churn: added=%d deleted=%d", client.added, client.deleted)
			}
			if repo.updated != nil {
				t.Fatalf("node was relinked: %+v", repo.updated)
			}
			if len(resync.ids) != 1 || resync.ids[0] != 100 {
				t.Fatalf("members must still be re-provisioned, got %v", resync.ids)
			}
		})
	}
}

// 3X-UI is unchanged: a failed GetInbound still means "missing", so the inbound
// is rebuilt from the snapshot, the routes cleared first, and the node relinked.
func TestXUIRecreateStillRebuildsAMissingInbound(t *testing.T) {
	cli := &recreateClient{inbounds: map[int]*ports.Inbound{}, nextID: 12}
	svc, repo, _, routeCalls := opsRecreateFixture(cli)
	if err := svc.RecreateInboundOnServer(context.Background(), 1); err != nil {
		t.Fatalf("err = %v", err)
	}
	if *routeCalls != 1 {
		t.Fatalf("route cleanup calls = %d, want 1", *routeCalls)
	}
	if repo.updated == nil || repo.updated.InboundID != 12 {
		t.Fatalf("node not relinked to the new inbound: %+v", repo.updated)
	}
}

// A node converted from 3X-UI can still carry route lines. Native routes are
// never upstream and route_state dies with the row, so a failing route cleaner
// must not block the delete (it would fail validation, be cancelled as
// permanent, and leave the row disabled).
func TestNativeNodeDeleteIgnoresRouteCleanup(t *testing.T) {
	svc, repo, cleaner := opsDeleteFixture(&opsNativeClient{getErr: ports.ErrNativePanelSnapshotMissing})
	calls := 0
	svc.SetRouteCleaner(func(context.Context, int64) error {
		calls++
		return domain.ErrValidation
	})
	if err := runNodeDelete(svc); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !repo.deleted || cleaner.unclaimed != 1 {
		t.Fatalf("deleted=%v unclaimed=%d", repo.deleted, cleaner.unclaimed)
	}
	if calls != 0 {
		t.Fatalf("route cleaner ran %d time(s) on a native delete", calls)
	}
	if cleaner.ensured != 0 || cleaner.delOwned != 0 || cleaner.deleted != 0 {
		t.Fatalf("upstream cleanup ran: ensured=%d delOwned=%d deleted=%d", cleaner.ensured, cleaner.delOwned, cleaner.deleted)
	}
}
