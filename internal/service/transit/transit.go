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
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
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
	nodes NodeLister
	repo  Repo
}

func New(nodes NodeLister, repo Repo) *Service { return &Service{nodes: nodes, repo: repo} }

// ForwardsFor returns the forwards the given entry server must run, in key
// order. A line whose landing cannot be reached (disabled, no address, no
// inbound port) is left out, and so is a forward the wire contract would
// refuse; one bad line never costs the server its other forwards.
func (s *Service) ForwardsFor(ctx context.Context, entryPanelID int64) ([]nodeprotocol.Forward, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("transit: list nodes: %w", err)
	}
	var forwards []nodeprotocol.Forward
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
				return nil, fmt.Errorf("transit: record line %s of node %d: %w", line.ID, node.ID, err)
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
		}
	}
	sort.Slice(forwards, func(i, j int) bool { return forwards[i].Key < forwards[j].Key })
	return forwards, nil
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
