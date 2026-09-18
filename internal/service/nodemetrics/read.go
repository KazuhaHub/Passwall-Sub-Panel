package nodemetrics

import (
	"context"
	"errors"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The read side: what the API serves, derived from what ingest stored.
//
// THE RAW COUNTERS NEVER REACH THE CLIENT. §11.1 is explicit — a uint64 above
// 2^53 loses precision in JavaScript, and a counter the browser rounds is one
// that disagrees with the panel that stored it. Everything below returns rates,
// percentages and gauges, which are the quantities the operator asked about
// anyway.

// Resolution selects the source of a history series.
type Resolution string

const (
	ResolutionMinute Resolution = "minute"
	ResolutionHour   Resolution = "hour"
)

// Bounds the history endpoint enforces (§11.2).
const (
	// MaxHistoryRange is the widest window any resolution may cover.
	MaxHistoryRange = 90 * 24 * time.Hour
	// MaxHistoryPoints is the most points any response may carry. It is what
	// makes the response size a function of the request rather than of how much
	// history happens to exist.
	MaxHistoryPoints = 2500
	// MaxMinuteRange is the widest window the raw table may be asked for: 2500
	// minutes of one-per-minute samples is exactly the point cap, so a wider
	// request could not be answered at minute resolution even if the rows existed.
	MaxMinuteRange = MaxHistoryPoints * time.Minute
)

// SeriesPoint is one plotted instant. Every metric is optional because every one
// of them can be a gap, and a gap is drawn as a break rather than as a zero.
type SeriesPoint struct {
	At              time.Time
	CoverageSeconds int
	Derived         Derived
}

// Snapshot is the current view: the latest sample plus the rates against the one
// before it.
type Snapshot struct {
	Observation *domain.NodeHostObservation
	// Sample is the stored row the values came from, for the scope and the
	// unavailable count.
	Sample *domain.NodeHostMetricSample
	// Previous is the baseline the rates were taken against. Nil means the
	// counter-derived values are gaps, which is what a node's first sample looks
	// like.
	Previous *domain.NodeHostMetricSample
	Derived  Derived
}

// Errors the API maps to fixed statuses.
var (
	// ErrNoSample means the agent has no history at all — an older node, or one
	// that has never reported telemetry.
	ErrNoSample = errors.New("nodemetrics: no sample for this agent")
	// ErrResolutionUnavailable means the requested resolution cannot answer the
	// requested window, and the caller must not be silently given the other one.
	ErrResolutionUnavailable = errors.New("nodemetrics: requested resolution does not cover this range")
)

// Current returns the newest snapshot with its rates.
//
// THE RATES COME FROM TWO DIFFERENT ROWS, and that is deliberate: the latest
// snapshot is the newest sample, and its rate needs the one before it. The
// repository is asked for the predecessor explicitly rather than the caller
// reusing the snapshot as its own baseline, which would produce a confident 0%
// for every rate.
func (s *Service) Current(ctx context.Context, agentID string) (*Snapshot, error) {
	observation, err := s.repo.Latest(ctx, agentID)
	if err != nil {
		return nil, err
	}
	samples, err := s.repo.RawRange(ctx, agentID, observation.ReceivedAt, observation.ReceivedAt, true)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		// A snapshot with no history row is possible: the latest write is not
		// transactional with the throttled history write. The snapshot is still
		// worth returning, with its gauges and no rates.
		return &Snapshot{Observation: observation}, nil
	}
	current := &samples[len(samples)-1]
	var previous *domain.NodeHostMetricSample
	if len(samples) > 1 {
		previous = &samples[0]
	}
	return &Snapshot{
		Observation: observation, Sample: current, Previous: previous,
		Derived: Derive(previous, current),
	}, nil
}

// History returns a series at the requested resolution.
func (s *Service) History(ctx context.Context, agentID string, from, to time.Time, resolution Resolution) ([]SeriesPoint, error) {
	if resolution == ResolutionHour {
		return s.hourlyHistory(ctx, agentID, from, to)
	}
	return s.minuteHistory(ctx, agentID, from, to)
}

// minuteHistory derives each raw sample against the one before it.
func (s *Service) minuteHistory(ctx context.Context, agentID string, from, to time.Time) ([]SeriesPoint, error) {
	// The predecessor is requested so the FIRST point of the window has a
	// baseline. Without it every chart would begin with a gap, which reads as an
	// outage that never happened.
	samples, err := s.repo.RawRange(ctx, agentID, from, to, true)
	if err != nil {
		return nil, err
	}
	points := make([]SeriesPoint, 0, len(samples))
	for index := range samples {
		sample := samples[index]
		if sample.ReceivedAt.Before(from) {
			// The baseline is not a data point.
			continue
		}
		var previous *domain.NodeHostMetricSample
		if index > 0 {
			previous = &samples[index-1]
		}
		points = append(points, SeriesPoint{
			At: sample.ReceivedAt, CoverageSeconds: int(sample.ReceivedAt.Sub(previousReceivedAt(previous, sample)).Seconds()),
			Derived: Derive(previous, &sample),
		})
	}
	return points, nil
}

func previousReceivedAt(previous *domain.NodeHostMetricSample, current domain.NodeHostMetricSample) time.Time {
	if previous == nil {
		return current.ReceivedAt
	}
	return previous.ReceivedAt
}

// hourlyHistory reads the rolled-up rows.
//
// IT NEVER MIXES THE TWO SOURCES. A bucket that has been rolled up is read from
// the hourly table and one that has not is computed from raw; reading both for
// the same bucket would double it, and the answer would depend on which query
// happened to run last.
func (s *Service) hourlyHistory(ctx context.Context, agentID string, from, to time.Time) ([]SeriesPoint, error) {
	rows, err := s.repo.HourlyRange(ctx, agentID, from, to)
	if err != nil {
		return nil, err
	}
	points := make([]SeriesPoint, 0, len(rows))
	for index := range rows {
		row := rows[index]
		if row.SampleCount == 0 {
			continue
		}
		points = append(points, SeriesPoint{
			At: row.BucketStart, CoverageSeconds: row.CoverageSeconds,
			Derived: derivedFromHourly(row),
		})
	}
	return points, nil
}

// derivedFromHourly projects an hourly row onto the same shape the minute series
// uses, so the client has one response shape rather than two.
func derivedFromHourly(row domain.NodeHostMetricHourly) Derived {
	var derived Derived
	derived.SystemCPUPercent = row.SystemCPUAverage
	derived.SystemIOWaitPercent = row.SystemIOWaitAverage
	derived.SystemStealPercent = row.SystemStealAverage
	derived.CgroupCPUCapacityPercent = row.CgroupCPUUsageAverage
	derived.CgroupCPUThrottledPeriodPercent = row.CgroupCPUThrottledPeriodAverage
	derived.LoadNormalized = row.LoadNormalizedAverage
	derived.MemoryUsedPercent = row.MemoryUsedAverage
	derived.DiskAvailableBytes = row.FilesystemAvailableMin
	derived.RXBps = row.NetworkRXBpsAverage
	derived.TXBps = row.NetworkTXBpsAverage
	derived.LinkUtilizationPercent = row.LinkUtilizationAverage
	derived.TCPRetransPercent = row.TCPRetransRatioAverage
	derived.CoreCPUPercent = row.CoreCPUAverage
	if row.CoreRSSMax != nil {
		// The hourly column is a float because it holds a weighted average; the
		// byte figure it came from is exact to 2^53, so the conversion is lossless
		// for any counter the wire can carry.
		value := uint64(*row.CoreRSSMax)
		derived.CoreRSSBytes = &value
	}
	return derived
}

// InterfacePoint is one interface's rates at one instant.
//
// IT IS ITS OWN SHAPE rather than a host SeriesPoint: an interface has no CPU,
// no memory and no filesystem, and reusing the host struct would mean forty
// fields that are always nil — which is indistinguishable from forty metrics that
// could not be read.
type InterfacePoint struct {
	At                     time.Time
	CoverageSeconds        int
	RXBps                  *float64
	TXBps                  *float64
	LinkUtilizationPercent *float64
	ErrorRatio             *float64
}

// InterfaceSeries is one interface's rates over a window.
type InterfaceSeries struct {
	Name            string
	Points          []InterfacePoint
	PointsWithRates int
}

// Interfaces returns one interface's series.
//
// THE NAME IS REQUIRED AND IS CHECKED AGAINST WHAT THE AGENT ACTUALLY REPORTED,
// so it can never reach a query as free text — an interface name is the host's
// layout, and the set of real ones is what the agent said.
//
// THE COUNTER EPOCH COMES FROM THE HOST SAMPLES, not from the interface rows:
// the rows carry the agent, the sample and the interface, and the boot id that
// makes two of them comparable lives on the sample. It is one extra read for the
// window rather than a lookup per row.
func (s *Service) Interfaces(ctx context.Context, agentID, name string, from, to time.Time) (InterfaceSeries, error) {
	names, err := s.repo.InterfaceNames(ctx, agentID)
	if err != nil {
		return InterfaceSeries{}, err
	}
	if !containsName(names, name) {
		return InterfaceSeries{}, domain.ErrNotFound
	}
	rows, err := s.repo.InterfaceRange(ctx, agentID, name, from, to)
	if err != nil {
		return InterfaceSeries{}, err
	}
	samples, err := s.repo.RawRange(ctx, agentID, from, to, false)
	if err != nil {
		return InterfaceSeries{}, err
	}
	bootIDBySample := make(map[string]string, len(samples))
	for index := range samples {
		bootIDBySample[samples[index].SampleID] = samples[index].BootID
	}

	series := InterfaceSeries{Name: name}
	for index := 1; index < len(rows); index++ {
		previous, current := rows[index-1], rows[index]
		derived := DeriveInterface(previous, current,
			bootIDBySample[previous.SampleID], bootIDBySample[current.SampleID])
		if derived.RXBps == nil {
			// A gap: an identity change, a reboot, or an interval outside the
			// derivation window. It is omitted rather than plotted as zero.
			continue
		}
		series.PointsWithRates++
		series.Points = append(series.Points, InterfacePoint{
			At: current.ReceivedAt, CoverageSeconds: int(current.ReceivedAt.Sub(previous.ReceivedAt).Seconds()),
			RXBps: derived.RXBps, TXBps: derived.TXBps,
			LinkUtilizationPercent: derived.LinkUtilizationPercent, ErrorRatio: derived.ErrorRatio,
		})
	}
	return series, nil
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
