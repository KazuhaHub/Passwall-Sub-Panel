package app

import (
	"context"
	"errors"
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

// The refresh cadence is geo_anomaly.infra_refresh_minutes, re-read every
// cycle: how soon a relay an admin just added stops being judged as a user's
// location. Unset (0) or unreadable means the shipped five minutes, and a
// value past an hour is clamped — a relay left un-excluded for longer would
// accuse everyone behind it. A settings outage falls back to the default
// rather than keeping a stale value, like every other domain-sanitised knob.
func TestInfraIntervalFromSettings(t *testing.T) {
	for _, c := range []struct {
		name    string
		minutes int
		err     error
		want    time.Duration
	}{
		{"a settings outage is the default", 2, errors.New("db down"), 5 * time.Minute},
		{"unset is the default", 0, nil, 5 * time.Minute},
		{"a configured value is used", 2, nil, 2 * time.Minute},
		{"past an hour is clamped", 999, nil, time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := infraIntervalFrom(ports.UISettings{GeoAnomalyInfraRefreshMinutes: c.minutes}, c.err)
			if got != c.want {
				t.Fatalf("infraIntervalFrom = %v, want %v", got, c.want)
			}
		})
	}
}
