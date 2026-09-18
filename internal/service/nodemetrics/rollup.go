package nodemetrics

import (
	"math"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The hourly rollup.
//
// IT AGGREGATES WHAT THE FORMULAS PRODUCED, NOT THE RAW COUNTERS. Subtracting an
// hour's first raw counter from its last would silently include every gap in
// between — a reboot, an agent outage, a counter reset — as if the machine had
// been running the whole time. Running §9's pair rules first turns those into
// gaps, and a gap contributes nothing to an average and no seconds to coverage.
//
// COVERAGE IS THE HONEST DENOMINATOR. An hour observed for ten minutes has an
// average of those ten minutes, and CoverageSeconds says so — which is what lets
// the UI avoid drawing a partially covered hour as a fully healthy one.

// weighted accumulates a time-weighted average.
type weighted struct {
	sum     float64
	seconds float64
	set     bool
}

func (w *weighted) add(value *float64, seconds float64) {
	if value == nil || seconds <= 0 {
		return
	}
	w.sum += *value * seconds
	w.seconds += seconds
	w.set = true
}

func (w *weighted) average() *float64 {
	if !w.set || w.seconds <= 0 {
		return nil
	}
	average := w.sum / w.seconds
	return &average
}

// maximum keeps the largest value seen.
type maximum struct{ value *float64 }

func (m *maximum) add(value *float64) {
	if value == nil {
		return
	}
	if m.value == nil || *value > *m.value {
		copied := *value
		m.value = &copied
	}
}

// minimumUint keeps the smallest unsigned value seen.
type minimumUint struct{ value *uint64 }

func (m *minimumUint) add(value *uint64) {
	if value == nil {
		return
	}
	if m.value == nil || *value < *m.value {
		copied := *value
		m.value = &copied
	}
}

// deltaSum accumulates a counter's increase over the hour.
type deltaSum struct {
	total uint64
	seen  bool
}

func (d *deltaSum) add(previous, current *uint64) {
	if previous == nil || current == nil || *current < *previous {
		return
	}
	d.total += *current - *previous
	d.seen = true
}

// addUint accumulates a pair of required counters, which the interface rows use:
// they are always present, so there is no absence to propagate.
func (d *deltaSum) addUint(previous, current uint64) {
	if current < previous {
		return
	}
	d.total += current - previous
	d.seen = true
}

func (d *deltaSum) valueOrNil() *uint64 {
	if !d.seen {
		return nil
	}
	total := d.total
	return &total
}

// bucketAccumulator gathers one hour's series.
type bucketAccumulator struct {
	coverageSeconds float64
	sampleCount     int

	systemCPU    weighted
	systemCPUMax maximum
	iowait       weighted
	iowaitMax    maximum
	steal        weighted
	stealMax     maximum

	cgroupUsage        weighted
	cgroupUsageMax     maximum
	cgroupThrottled    weighted
	cgroupThrottledMax maximum
	throttledTime      deltaSum

	loadNormalized    weighted
	loadNormalizedMax maximum

	memoryUsed         weighted
	memoryUsedMax      maximum
	memoryAvailableMin minimumUint

	cgroupMemoryUsed    weighted
	cgroupMemoryUsedMax maximum
	oomDelta            deltaSum
	oomKillDelta        deltaSum

	filesystemAvailableMin minimumUint
	inodeAvailableMin      minimumUint

	rxBps              weighted
	rxBpsMax           maximum
	txBps              weighted
	txBpsMax           maximum
	linkUtilization    weighted
	linkUtilizationMax maximum
	networkErrors      deltaSum
	networkDrops       deltaSum

	tcpRetransRatio    weighted
	tcpRetransRatioMax maximum
	tcpRetransDelta    deltaSum

	coreCPU    weighted
	coreCPUMax maximum
	coreRSS    weighted
	coreRSSMax maximum

	conntrackRatio    weighted
	conntrackRatioMax maximum

	syncRTT      weighted
	syncRTTMax   maximum
	syncFailures deltaSum
	coreRestarts deltaSum
	agentFDMax   *uint64
	coreFDMax    *uint64
	agentCPU     weighted
	agentCPUMax  maximum
	agentRSS     weighted
	agentRSSMax  maximum
}

// RollupHour aggregates one UTC hour.
//
// predecessor is the newest sample BEFORE the bucket, and it is what makes the
// first interval of the hour computable — without it the earliest part of every
// hour would be a gap for no reason but the window's edge.
//
// INTERVALS ARE APPORTIONED ACROSS THE BOUNDARY. An interval from 11:59:30 to
// 12:00:30 is half an hour's worth in each bucket, and assigning it wholly to
// either would make one hour look busier than it was while making the other look
// quieter — a distortion that repeats every hour and never averages out.
func RollupHour(agentID string, bucketStart time.Time, predecessor *domain.NodeHostMetricSample, samples []domain.NodeHostMetricSample, interfaces []domain.NodeInterfaceMetricSample) domain.NodeHostMetricHourly {
	bucketStart = bucketStart.UTC()
	bucketEnd := bucketStart.Add(time.Hour)
	accumulator := &bucketAccumulator{}
	// Interface rows are grouped by the sample they belong to, so each adjacent
	// pair can be aligned with its own rows rather than with the window's.
	interfacesBySample := map[string][]domain.NodeInterfaceMetricSample{}
	for index := range interfaces {
		row := interfaces[index]
		interfacesBySample[row.SampleID] = append(interfacesBySample[row.SampleID], row)
	}

	previous := predecessor
	for index := range samples {
		current := &samples[index]
		if previous != nil {
			if overlap, seconds := intervalOverlap(previous.ReceivedAt, current.ReceivedAt, bucketStart, bucketEnd); overlap {
				accumulator.accumulate(previous, current, seconds)
				accumulator.accumulateNetwork(
					interfacesBySample[previous.SampleID], interfacesBySample[current.SampleID],
					previous.BootID, current.BootID, seconds)
			}
		}
		previous = current
	}
	accumulator.sampleCount = len(samples)
	if accumulator.coverageSeconds > 3600 {
		// The union of valid intervals can exceed the hour only if an interval
		// was apportioned twice, which is a bug rather than a measurement.
		accumulator.coverageSeconds = 3600
	}
	return accumulator.build(agentID, bucketStart)
}

// intervalOverlap reports how much of an interval falls inside the bucket, in
// seconds.
//
// AN INTERVAL THE DERIVATION REFUSES IS NOT COVERAGE EITHER. The window check
// comes first, on the FULL span rather than the clipped one, because that is the
// span §9's rules judge: counting a half-hour outage as coverage would report a
// fully covered hour whose every average came from nothing.
func intervalOverlap(from, to, bucketStart, bucketEnd time.Time) (bool, float64) {
	if !to.After(from) {
		return false, 0
	}
	if span := to.Sub(from); span < minDeriveInterval || span > maxDeriveInterval {
		return false, 0
	}
	start := from
	if start.Before(bucketStart) {
		start = bucketStart
	}
	end := to
	if end.After(bucketEnd) {
		end = bucketEnd
	}
	if !end.After(start) {
		return false, 0
	}
	return true, end.Sub(start).Seconds()
}

func (a *bucketAccumulator) accumulate(previous, current *domain.NodeHostMetricSample, seconds float64) {
	derived := Derive(previous, current)
	a.coverageSeconds += seconds

	a.systemCPU.add(derived.SystemCPUPercent, seconds)
	a.systemCPUMax.add(derived.SystemCPUPercent)
	a.iowait.add(derived.SystemIOWaitPercent, seconds)
	a.iowaitMax.add(derived.SystemIOWaitPercent)
	a.steal.add(derived.SystemStealPercent, seconds)
	a.stealMax.add(derived.SystemStealPercent)

	a.cgroupUsage.add(derived.CgroupCPUCapacityPercent, seconds)
	a.cgroupUsageMax.add(derived.CgroupCPUCapacityPercent)
	a.cgroupThrottled.add(derived.CgroupCPUThrottledPeriodPercent, seconds)
	a.cgroupThrottledMax.add(derived.CgroupCPUThrottledPeriodPercent)
	a.throttledTime.add(previous.CgroupCPUThrottledUS, current.CgroupCPUThrottledUS)

	a.loadNormalized.add(derived.LoadNormalized, seconds)
	a.loadNormalizedMax.add(derived.LoadNormalized)

	a.memoryUsed.add(derived.MemoryUsedPercent, seconds)
	a.memoryUsedMax.add(derived.MemoryUsedPercent)
	a.memoryAvailableMin.add(current.SystemMemoryAvailableBytes)

	a.cgroupMemoryUsed.add(derived.CgroupMemoryUsedPercent, seconds)
	a.cgroupMemoryUsedMax.add(derived.CgroupMemoryUsedPercent)
	a.oomDelta.add(previous.CgroupOOMEvents, current.CgroupOOMEvents)
	a.oomKillDelta.add(previous.CgroupOOMKillEvents, current.CgroupOOMKillEvents)

	a.filesystemAvailableMin.add(current.FilesystemAvailableBytes)
	a.inodeAvailableMin.add(current.FilesystemAvailableInodes)

	a.tcpRetransRatio.add(derived.TCPRetransPercent, seconds)
	a.tcpRetransRatioMax.add(derived.TCPRetransPercent)
	a.tcpRetransDelta.add(previous.TCPRetransSegments, current.TCPRetransSegments)

	a.coreCPU.add(derived.CoreCPUPercent, seconds)
	a.coreCPUMax.add(derived.CoreCPUPercent)
	a.coreRSS.add(byteGauge(current.CoreRSSBytes), seconds)
	a.coreRSSMax.add(byteGauge(current.CoreRSSBytes))
	a.agentRSS.add(byteGauge(current.AgentRSSBytes), seconds)
	a.agentRSSMax.add(byteGauge(current.AgentRSSBytes))
	a.agentFDMax = larger(a.agentFDMax, current.AgentOpenFDs)
	a.coreFDMax = larger(a.coreFDMax, current.CoreOpenFDs)
	a.coreRestarts.add(previous.CoreRestartCount, current.CoreRestartCount)

	a.conntrackRatio.add(conntrackRatio(current), seconds)
	a.conntrackRatioMax.add(conntrackRatio(current))

	a.syncRTT.add(millis(current.LastRoundTripMS), seconds)
	a.syncRTTMax.add(millis(current.LastRoundTripMS))
	a.syncFailures.add(previous.SyncFailureCount, current.SyncFailureCount)
}

// accumulateNetwork folds one pair of interface sets into the hour.
//
// The server-wide throughput is the DEFAULT interfaces only, because that is what
// "this node's traffic" means — summing every interface would count a bridge and
// the veth behind it twice, and a container host always has both.
func (a *bucketAccumulator) accumulateNetwork(previousRows, currentRows []domain.NodeInterfaceMetricSample, previousEpoch, currentEpoch string, seconds float64) {
	if len(previousRows) == 0 || len(currentRows) == 0 {
		return
	}
	byIndex := make(map[int]domain.NodeInterfaceMetricSample, len(previousRows))
	for _, row := range previousRows {
		byIndex[row.InterfaceIndex] = row
	}
	derived := make(map[string]InterfaceDerived, len(currentRows))
	var previousDefaults, currentDefaults []string
	for _, row := range currentRows {
		if row.IsDefaultIPv4 || row.IsDefaultIPv6 {
			currentDefaults = append(currentDefaults, row.InterfaceName)
		}
		previousRow, exists := byIndex[row.InterfaceIndex]
		if !exists {
			continue
		}
		derived[row.InterfaceName] = DeriveInterface(previousRow, row, previousEpoch, currentEpoch)
		// Errors and drops from both directions, because a transmit drop is as
		// much a network fault as a receive one.
		a.networkErrors.addUint(previousRow.RXErrors+previousRow.TXErrors, row.RXErrors+row.TXErrors)
		a.networkDrops.addUint(previousRow.RXDropped+previousRow.TXDropped, row.RXDropped+row.TXDropped)
	}
	for _, row := range previousRows {
		if row.IsDefaultIPv4 || row.IsDefaultIPv6 {
			previousDefaults = append(previousDefaults, row.InterfaceName)
		}
	}
	total := HostThroughput(previousDefaults, currentDefaults, nil, derived)
	if total == nil {
		return
	}
	// The two directions are reported separately and the utilisation is the peak
	// of them, matching §9.4's full-duplex rule at the hourly level too.
	var rx, tx *float64
	var utilization *float64
	for _, name := range currentDefaults {
		value, exists := derived[name]
		if !exists || value.RXBps == nil || value.TXBps == nil {
			return
		}
		sumRX, sumTX := valueOrZero(rx)+*value.RXBps, valueOrZero(tx)+*value.TXBps
		rx, tx = &sumRX, &sumTX
		if value.LinkUtilizationPercent != nil {
			peak := *value.LinkUtilizationPercent
			if utilization == nil || peak > *utilization {
				utilization = &peak
			}
		}
	}
	a.rxBps.add(rx, seconds)
	a.rxBpsMax.add(rx)
	a.txBps.add(tx, seconds)
	a.txBpsMax.add(tx)
	a.linkUtilization.add(utilization, seconds)
	a.linkUtilizationMax.add(utilization)
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

// byteGauge widens an unsigned byte count into the float the weighted average
// uses. It is exact up to 2^53 bytes, which is four petabytes — beyond any
// counter this protocol can carry, since the wire caps them at MaxInt64.
func byteGauge(value *uint64) *float64 {
	if value == nil {
		return nil
	}
	converted := float64(*value)
	return &converted
}

func millis(value *uint64) *float64 {
	if value == nil {
		return nil
	}
	converted := float64(*value)
	return &converted
}

func conntrackRatio(sample *domain.NodeHostMetricSample) *float64 {
	if sample.ConntrackCurrent == nil || sample.ConntrackLimit == nil || *sample.ConntrackLimit == 0 {
		return nil
	}
	ratio := 100 * float64(*sample.ConntrackCurrent) / float64(*sample.ConntrackLimit)
	return &ratio
}

func larger(current, candidate *uint64) *uint64 {
	if candidate == nil {
		return current
	}
	if current == nil || *candidate > *current {
		copied := *candidate
		return &copied
	}
	return current
}

func (a *bucketAccumulator) build(agentID string, bucketStart time.Time) domain.NodeHostMetricHourly {
	hourly := domain.NodeHostMetricHourly{
		BucketStart: bucketStart, AgentID: agentID,
		SampleCount: a.sampleCount, CoverageSeconds: int(math.Round(a.coverageSeconds)),

		SystemCPUAverage: a.systemCPU.average(), SystemCPUMax: a.systemCPUMax.value,
		SystemIOWaitAverage: a.iowait.average(), SystemIOWaitMax: a.iowaitMax.value,
		SystemStealAverage: a.steal.average(), SystemStealMax: a.stealMax.value,

		CgroupCPUUsageAverage: a.cgroupUsage.average(), CgroupCPUUsageMax: a.cgroupUsageMax.value,
		CgroupCPUThrottledPeriodAverage: a.cgroupThrottled.average(),
		CgroupCPUThrottledPeriodMax:     a.cgroupThrottledMax.value,
		CgroupCPUThrottledTimeSum:       a.throttledTime.valueOrNil(),

		LoadNormalizedAverage: a.loadNormalized.average(), LoadNormalizedMax: a.loadNormalizedMax.value,

		MemoryUsedAverage: a.memoryUsed.average(), MemoryUsedMax: a.memoryUsedMax.value,
		MemoryAvailableMin: a.memoryAvailableMin.value,

		CgroupMemoryUsedAverage: a.cgroupMemoryUsed.average(), CgroupMemoryUsedMax: a.cgroupMemoryUsedMax.value,
		CgroupOOMDelta: a.oomDelta.valueOrNil(), CgroupOOMKillDelta: a.oomKillDelta.valueOrNil(),

		FilesystemAvailableMin:       a.filesystemAvailableMin.value,
		FilesystemInodesAvailableMin: a.inodeAvailableMin.value,

		NetworkRXBpsAverage: a.rxBps.average(), NetworkRXBpsMax: a.rxBpsMax.value,
		NetworkTXBpsAverage: a.txBps.average(), NetworkTXBpsMax: a.txBpsMax.value,
		LinkUtilizationAverage: a.linkUtilization.average(), LinkUtilizationMax: a.linkUtilizationMax.value,
		NetworkErrorsDelta: a.networkErrors.valueOrNil(), NetworkDropsDelta: a.networkDrops.valueOrNil(),

		TCPRetransRatioAverage: a.tcpRetransRatio.average(), TCPRetransRatioMax: a.tcpRetransRatioMax.value,
		TCPRetransDelta: a.tcpRetransDelta.valueOrNil(),

		AgentCPUAverage: a.agentCPU.average(), AgentCPUMax: a.agentCPUMax.value,
		AgentRSSAverage: a.agentRSS.average(), AgentRSSMax: a.agentRSSMax.value,
		AgentFDMax: a.agentFDMax,

		CoreCPUAverage: a.coreCPU.average(), CoreCPUMax: a.coreCPUMax.value,
		CoreRSSAverage: a.coreRSS.average(), CoreRSSMax: a.coreRSSMax.value,
		CoreFDMax: a.coreFDMax, CoreRestartDelta: a.coreRestarts.valueOrNil(),

		ConntrackRatioAverage: a.conntrackRatio.average(), ConntrackRatioMax: a.conntrackRatioMax.value,

		SyncRTTAverage: a.syncRTT.average(), SyncRTTMax: a.syncRTTMax.value,
		SyncFailureDelta: a.syncFailures.valueOrNil(),
	}
	return hourly
}

// RollupBucketStart floors an instant to its UTC hour.
//
// UTC RATHER THAN THE PANEL TIMEZONE, deliberately: the buckets are storage, and
// a display timezone that changes would otherwise re-bucket history that has
// already been aggregated. The panel renders the UTC buckets in local time
// instead, which is a presentation concern and reversible.
func RollupBucketStart(at time.Time) time.Time {
	return at.UTC().Truncate(time.Hour)
}
