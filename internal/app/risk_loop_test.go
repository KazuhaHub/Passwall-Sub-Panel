package app

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The risk worker's cadence is risk.refresh_interval_minutes, re-read after
// every refresh. Unset (0) or unreadable means the shipped hour. Under ten
// minutes is raised — each run re-reads a week of fetches and five weeks of
// hourly rows per account — and past a day is lowered, so a flag is at most
// a day stale. A settings outage falls back to the default rather than a
// value it cannot read, like every other domain-sanitised knob.
func TestRiskIntervalFromSettings(t *testing.T) {
	for _, c := range []struct {
		name    string
		minutes int
		err     error
		want    time.Duration
	}{
		{"a settings outage is the default", 30, errors.New("db down"), time.Hour},
		{"unset is the default", 0, nil, time.Hour},
		{"a configured value is used", 30, nil, 30 * time.Minute},
		{"under ten minutes is raised", 5, nil, 10 * time.Minute},
		{"past a day is lowered", 5000, nil, 24 * time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := riskIntervalFrom(ports.UISettings{RiskRefreshIntervalMinutes: c.minutes}, c.err)
			if got != c.want {
				t.Fatalf("riskIntervalFrom = %v, want %v", got, c.want)
			}
		})
	}
}

// The first run waits risk.first_delay_minutes after start, read once when
// the loop is launched (a change takes effect on the next restart). Unset or
// unreadable is the shipped two minutes; past an hour is lowered, or a fresh
// install's risk view would stay empty that long.
func TestRiskFirstDelayFromSettings(t *testing.T) {
	for _, c := range []struct {
		name    string
		minutes int
		err     error
		want    time.Duration
	}{
		{"a settings outage is the default", 10, errors.New("db down"), 2 * time.Minute},
		{"unset is the default", 0, nil, 2 * time.Minute},
		{"a configured value is used", 10, nil, 10 * time.Minute},
		{"past an hour is lowered", 999, nil, time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := riskFirstDelayFrom(ports.UISettings{RiskFirstDelayMinutes: c.minutes}, c.err)
			if got != c.want {
				t.Fatalf("riskFirstDelayFrom = %v, want %v", got, c.want)
			}
		})
	}
}
