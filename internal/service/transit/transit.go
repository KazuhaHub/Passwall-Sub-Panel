// Package transit publishes managed relay lines to the Passwall Node servers
// that run them and records what those servers confirm.
//
// A managed relay line is an ordinary relay line (domain.RelayLine) whose
// EntryPanelID names a Passwall Node server. That server relays the line's
// port, unchanged, to the landing node. The line enters subscriptions only
// once the entry has confirmed exactly the forward the line asks for
// (RelayLine.Publishable); until then it is withheld, never shown as working.
package transit

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/linkprobe"
)

// NodeLister is the slice of ports.NodeRepo this service reads.
type NodeLister interface {
	List(ctx context.Context) ([]*domain.Node, error)
}

// Repo is the managed relay state store (sqlstore transit repo).
type Repo interface {
	Ensure(ctx context.Context, nodeID int64, relayID string) (domain.TransitForward, error)
	SetApplied(ctx context.Context, id int64, applied domain.ManagedForward, at time.Time) (bool, error)
	ClearApplied(ctx context.Context, id int64) (bool, error)
	RecordCounters(ctx context.Context, id int64, up, down int64, epoch uint64) error
}

type Service struct {
	nodes  NodeLister
	repo   Repo
	links  LinkRepo
	agents AgentLister
	// panelGone says a server no longer exists; nil knows of none that do not.
	panelGone func(ctx context.Context, panelID int64) bool
	// serverLock is the fronts' per-server lock, which a save that drops or binds
	// a tunnel holds; nil takes none.
	serverLock func(panelID int64) func()
	// reserved are link ports PlanLink promised to a pair of servers.
	// allocMu makes choosing and using a link port one critical section (PlanLink,
	// a new link, the allocation); reservedMu guards the map alone and is never
	// held across a call out, because the port check reads the reservations too.
	allocMu    sync.Mutex
	reservedMu sync.Mutex
	reserved   map[[2]int64]reservation
	// portInUse says something on a server holds a port (its listeners, forwards,
	// shared ports and loopbacks); nil knows only what the nodes and links say.
	portInUse func(ctx context.Context, panelID int64, port int) bool
	// now is the clock the reservations expire by.
	now func() time.Time
}

// reservationTTL is how long a link port PlanLink promised stays promised.
const reservationTTL = 10 * time.Minute

type reservation struct {
	port    int
	expires time.Time
}

// WithPortCheck gives the service a way to ask whether a port on a server is
// held by anything (fronts' shared ports and loopbacks included), which a link
// port must also avoid.
func (s *Service) WithPortCheck(inUse func(ctx context.Context, panelID int64, port int) bool) *Service {
	s.portInUse = inUse
	return s
}

// WithServerLock gives the service the lock of a server that node saves and the
// routes' cleanup hold, so that a sync does not make a tunnel that was dropped
// a moment ago (LinksFor).
func (s *Service) WithServerLock(lock func(panelID int64) func()) *Service {
	s.serverLock = lock
	return s
}

// lockPair takes the lock of both servers of a tunnel, lower id first (the order
// everything else takes them in).
func (s *Service) lockPair(a, b int64) func() {
	if s.serverLock == nil {
		return func() {}
	}
	if a > b {
		a, b = b, a
	}
	first := s.serverLock(a)
	if a == b {
		return first
	}
	second := s.serverLock(b)
	return func() { second(); first() }
}

// WithPanelCheck lets the service know which servers have been deleted, so that
// no tunnel is ever made to one.
func (s *Service) WithPanelCheck(gone func(ctx context.Context, panelID int64) bool) *Service {
	s.panelGone = gone
	return s
}

func New(nodes NodeLister, repo Repo) *Service { return &Service{nodes: nodes, repo: repo} }

// ForwardsFor returns the forwards the given entry server must run, in key
// order. A line whose landing cannot be reached (disabled, no address, no
// inbound port) is left out, and so is a forward the wire contract would
// refuse; one bad line never costs the server its other forwards.
func (s *Service) ForwardsFor(ctx context.Context, entryPanelID int64) ([]nodeprotocol.Forward, error) {
	forwards, _, err := s.ForwardsWithProbeMethods(ctx, entryPanelID)
	return forwards, err
}

// ForwardsWithProbeMethods is ForwardsFor together with the method each
// forward is probed with (transit.probe.v1), decided from its landing node.
func (s *Service) ForwardsWithProbeMethods(ctx context.Context, entryPanelID int64) ([]nodeprotocol.Forward, map[nodeprotocol.ForwardKey]string, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("transit: list nodes: %w", err)
	}
	var forwards []nodeprotocol.Forward
	methods := map[nodeprotocol.ForwardKey]string{}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, line := range node.Relays {
			if line.EntryPanelID != entryPanelID {
				continue
			}
			desired, ok := domain.DesiredManagedForward(node, line)
			if !ok {
				continue
			}
			row, err := s.repo.Ensure(ctx, node.ID, line.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("transit: record line %s of node %d: %w", line.ID, node.ID, err)
			}
			forward := nodeprotocol.Forward{
				Key: nodeprotocol.NewForwardKey(row.ID), Port: desired.Port,
				Target: desired.Target, TargetPort: desired.TargetPort,
				// The landing's protocol decides whether UDP is used; relaying
				// both costs nothing when it is not.
				Network: nodeprotocol.ForwardNetworkTCPUDP,
			}
			if err := nodeprotocol.ValidateForward(forward); err != nil {
				log.Warn("managed relay line withheld: its forward is invalid",
					"node_id", node.ID, "relay_id", line.ID, "entry_panel_id", entryPanelID, "err", err)
				continue
			}
			forwards = append(forwards, forward)
			// No SNI front targets a forward yet (fronts come with the
			// front editor); when they do, the front route decides here.
			methods[forward.Key] = linkprobe.Method(linkprobe.UDPOnlyProtocol(node.DesiredProtocol), false)
		}
	}
	sort.Slice(forwards, func(i, j int) bool { return forwards[i].Key < forwards[j].Key })
	return forwards, methods, nil
}

// Confirm records, for every forward in the config the entry now holds, what
// it reported: applied stores the forward as confirmed, anything else (or no
// status at all) withdraws a previous confirmation. It is called only when
// the entry holds the current config, so the body is exactly what the
// statuses describe. It reports whether any confirmation changed, so the
// caller refreshes subscriptions on a transition only.
func (s *Service) Confirm(ctx context.Context, entryPanelID int64, held *nodeprotocol.Transit, statuses map[string]nodeprotocol.ObjectStatus, now time.Time) (bool, error) {
	if held == nil {
		return false, nil
	}
	changed := false
	for _, forward := range held.Forwards {
		id, err := forward.Key.RowID()
		if err != nil {
			continue
		}
		status, reported := statuses[string(forward.Key)]
		var moved bool
		if reported && status.State == nodeprotocol.ObjectApplied {
			moved, err = s.repo.SetApplied(ctx, id, domain.ManagedForward{
				EntryPanelID: entryPanelID, Port: forward.Port, Target: forward.Target, TargetPort: forward.TargetPort,
			}, now)
		} else {
			moved, err = s.repo.ClearApplied(ctx, id)
		}
		if err != nil {
			return changed, fmt.Errorf("transit: confirm %s: %w", forward.Key, err)
		}
		changed = changed || moved
	}
	return changed, nil
}

// RecordCounters books the entry's forward counters. Only forwards in the
// config the panel sent this entry are accepted: a key is a global row id,
// and an entry must not be able to book traffic against another server's
// line.
func (s *Service) RecordCounters(ctx context.Context, sent *nodeprotocol.Transit, counters []nodeprotocol.ForwardCounters) error {
	if sent == nil || len(counters) == 0 {
		return nil
	}
	owned := make(map[nodeprotocol.ForwardKey]bool, len(sent.Forwards))
	for _, forward := range sent.Forwards {
		owned[forward.Key] = true
	}
	for _, counter := range counters {
		if !owned[counter.Key] || !counter.Present {
			continue
		}
		id, err := counter.Key.RowID()
		if err != nil {
			continue
		}
		if err := s.repo.RecordCounters(ctx, id, counter.UpBytes, counter.DownBytes, counter.CounterEpoch); err != nil {
			return fmt.Errorf("transit: record %s counters: %w", counter.Key, err)
		}
	}
	return nil
}
