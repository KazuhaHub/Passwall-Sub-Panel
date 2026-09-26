package app

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
)

// nodeLister is a NodeRepo that only lists; the refresh needs nothing else.
type nodeLister struct {
	ports.NodeRepo
	nodes []*domain.Node
}

func (r nodeLister) List(context.Context) ([]*domain.Node, error) { return r.nodes, nil }

// The first traffic poll runs one interval after boot, and the relay
// exclusion must already be in place by then: a loop that waited out its
// first tick would judge that poll with no infrastructure excluded. So the
// loop refreshes as soon as it starts, observed through the gauge it sets.
func TestInfraAddressLoopRefreshesAtStart(t *testing.T) {
	metrics.InfraAddresses.Set(0)

	svc := traffic.New(nil, nil, nil, nodeLister{nodes: []*domain.Node{
		{ID: 1, Enabled: true, ServerAddress: "203.0.113.1"},
	}}, nil, nil, nil)
	a := &App{traffic: svc, operationGate: operationgate.New()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); a.runInfraAddressLoop(ctx) }()

	var got int64
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, g := range metrics.Take().Gauges {
			if g.Name == "psp_infra_addresses" {
				got = g.Value
			}
		}
		if got != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != 1 {
		t.Fatalf("psp_infra_addresses = %d, want 1 — the loop did not refresh at start", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("infra address loop did not exit on context cancel")
	}
}
