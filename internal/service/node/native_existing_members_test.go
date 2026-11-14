package node

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// nativeClient stands in for pspnode.Client: a native panel applies
// asynchronously, and GetInbound answers from the agent's last full report.
type nativeClient struct {
	ports.XUIClient
	getErr error
}

func (nativeClient) ApplyIsAsynchronous() bool { return true }
func (c nativeClient) GetInbound(context.Context, int) (*ports.Inbound, error) {
	return nil, c.getErr
}

// xuiClient stands in for a 3X-UI client whose inbound lookup fails.
type xuiClient struct {
	ports.XUIClient
	getErr error
}

func (c xuiClient) GetInbound(context.Context, int) (*ports.Inbound, error) {
	return nil, c.getErr
}

func newMembersService(c ports.XUIClient, tasks *recordingTasks) *Service {
	return &Service{pool: stubXUIPool{c: c}, groups: oneAllGroup{}, users: twoMembers{}, tasks: tasks}
}

// A node created on a native panel must enrol the existing group members
// whether or not the agent has reported the node yet. The resync writes their
// shared clients and attachments before any read-back needs the agent's report,
// and nothing retried the enqueue when it was refused.
func TestSyncExistingUsersToNativeNodeDoesNotWaitForTheAgentReport(t *testing.T) {
	for name, getErr := range map[string]error{
		"before the agent's first full report":    ports.ErrNativePanelSnapshotMissing,
		"agent reporting, new node not in it yet": domain.ErrNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			tasks := &recordingTasks{}
			svc := newMembersService(nativeClient{getErr: getErr}, tasks)
			n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20, DesiredProtocol: "vless"}
			if err := svc.syncExistingUsersToNode(context.Background(), n); err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(tasks.created) != 2 {
				t.Fatalf("want 2 user_resync tasks for the existing members, got %d", len(tasks.created))
			}
		})
	}
}

// The native path still refuses a node whose protocol PSP cannot provision.
func TestSyncExistingUsersToNativeNodeSkipsAnUnsupportedProtocol(t *testing.T) {
	tasks := &recordingTasks{}
	svc := newMembersService(nativeClient{getErr: ports.ErrNativePanelSnapshotMissing}, tasks)
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20, DesiredProtocol: "dokodemo-door"}
	if err := svc.syncExistingUsersToNode(context.Background(), n); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(tasks.created) != 0 {
		t.Fatalf("an unsupported protocol must not enqueue resyncs, got %d", len(tasks.created))
	}
}

// 3X-UI keeps reading the live inbound: there it is the source of the protocol
// and the inbound exists by the time the node row does.
func TestSyncExistingUsersToXUINodeStillNeedsTheInbound(t *testing.T) {
	tasks := &recordingTasks{}
	svc := newMembersService(xuiClient{getErr: domain.ErrNotFound}, tasks)
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20, DesiredProtocol: "vless"}
	err := svc.syncExistingUsersToNode(context.Background(), n)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want the inbound lookup error", err)
	}
	if len(tasks.created) != 0 {
		t.Fatalf("got %d tasks", len(tasks.created))
	}
}
