package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The traffic loop used to capture its interval at boot, so changing
// cron_traffic_pull_minutes was a silent no-op on the loop that meters traffic,
// enforces quota and collects live IPs — the three things the setting exists to
// pace. Every other periodic loop re-reads it.
//
// The half that made it visible: the diagnostics page read the same settings
// row, believed a lowered interval was already in force, judged a four-minute
// window "settled" against it, and reported a critical "the traffic poll is
// dead" about a healthy process that simply had not reached its five-minute
// tick. So the loop now publishes the cadence it HOLDS.
func TestNextTrafficInterval(t *testing.T) {
	const current = 5 * time.Minute

	for _, tc := range []struct {
		name    string
		minutes int
		err     error
		want    time.Duration
	}{
		{"an admin's change is adopted", 1, nil, time.Minute},
		{"an unchanged setting is a no-op", 5, nil, current},
		// Each of these would otherwise reach time.Ticker.Reset, which panics
		// on a non-positive duration, and would take the whole loop down.
		{"a settings outage keeps the current cadence", 1, errors.New("db down"), current},
		{"an unset value keeps the current cadence", 0, nil, current},
		{"a negative value keeps the current cadence", -3, nil, current},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nextTrafficInterval(current, ports.UISettings{CronTrafficPullMinutes: tc.minutes}, tc.err)
			if got != tc.want {
				t.Fatalf("nextTrafficInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

// The cadence has to be readable from outside the process, because that is the
// number a diagnostics reader judges every zero against. Asserted on the boot
// publish, which happens before the loop's first sleep.
func TestTrafficLoopPublishesItsCadence(t *testing.T) {
	metrics.PollIntervalMS.Set(0)

	a := &App{settings: staticSettings{minutes: 60}, trafficInterval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); a.runTrafficLoop(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	var got int64
	for time.Now().Before(deadline) {
		for _, g := range metrics.Take().Gauges {
			if g.Name == "psp_poll_interval_ms" {
				got = g.Value
			}
		}
		if got != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if want := time.Hour.Milliseconds(); got != want {
		t.Fatalf("psp_poll_interval_ms = %d, want %d", got, want)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("traffic loop did not exit on context cancel")
	}
}

type staticSettings struct{ minutes int }

func (s staticSettings) Load(_ context.Context, d ports.UISettings) (ports.UISettings, error) {
	d.CronTrafficPullMinutes = s.minutes
	return d, nil
}
func (staticSettings) Save(context.Context, ports.UISettings) error { return nil }
