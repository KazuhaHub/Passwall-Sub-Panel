package nodemetrics

import (
	"math"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Derivation: the panel's own formulas, as pure functions over two stored
// samples.
//
// EVERYTHING HERE TAKE TWO SAMPLES OR ONE, AND NOTHING ELSE. No database, no
// clock, no configuration — so every rule in the spec's §9 can be exercised by a
// table of inputs, and a formula cannot quietly depend on when it was called.
//
// THE OUTPUT IS POINTERS, AND A NIL IS A GAP. A rate needs two samples of the
// same epoch close enough together to mean anything; when that does not hold,
// there is no value. Substituting zero would draw a flat line through a reboot
// and a healthy-looking host through an outage, which is worse than a break in
// the chart because it is invisible.

// DeriveWindow holds the bounds a pair must satisfy for a difference to be
// meaningful.
const (
	// minDeriveInterval drops pairs closer together than any real poll could
	// produce. Two samples of the same instant divide by a denominator that is
	// noise, and the resulting spike is an artefact rather than a measurement.
	minDeriveInterval = 5 * time.Second
	// maxDeriveInterval drops pairs too far apart to attribute to one interval:
	// an agent offline for half an hour did whatever it did, and spreading that
	// over thirty minutes would invent a rate nothing observed.
	maxDeriveInterval = 15 * time.Minute
)

// Derived is one instant's computed values.
//
// The names follow the API surface rather than the columns, because this is what
// the API and the charts consume; the raw counters never leave the persistence
// layer.
type Derived struct {
	// CPUPercent is the primary figure: the cgroup's own capacity when one is
	// known, otherwise the system's. CPUScope says which, and is nil exactly when
	// CPUPercent is.
	CPUPercent                      *float64
	CPUScope                        *string
	SystemCPUPercent                *float64
	SystemIOWaitPercent             *float64
	SystemStealPercent              *float64
	CgroupCPUCoresPercent           *float64
	CgroupCPUQuotaPercent           *float64
	CgroupCPUCapacityPercent        *float64
	CgroupCPUThrottledPeriodPercent *float64
	LoadNormalized                  *float64
	CapacityCores                   *float64

	MemoryUsedPercent *float64
	MemoryScope       *string
	// Gauge-derived, so they need no predecessor.
	MemoryUsedBytes         *uint64
	CgroupMemoryUsedPercent *float64
	DiskUsedPercent         *float64
	DiskAvailableBytes      *uint64
	InodeUsedPercent        *float64

	RXBps                  *float64
	TXBps                  *float64
	LinkUtilizationPercent *float64
	NetworkErrorRatio      *float64

	TCPRetransPercent *float64
	// TCPRetransDelta is reported even when the ratio is not evaluated, because a
	// small absolute number is still a fact; only the RATIO needs a denominator
	// worth dividing by.
	TCPRetransDelta *uint64

	CoreCPUPercent  *float64
	AgentCPUPercent *float64
	CoreRSSBytes    *uint64
}

const (
	CPUScopeSystem       = "system"
	CPUScopeCgroupQuota  = "cgroup_quota"
	CPUScopeCgroupCPUSet = "cgroup_cpuset"

	MemoryScopeSystem = "system"
	MemoryScopeCgroup = "cgroup"
)

// TCPRetransRatioDenominatorFloor is the out-segment delta below which the
// retransmission ratio is not evaluated.
//
// It is a floor rather than a rule about correctness: one retransmission out of
// twenty segments is 5%, and it is not a fact about the network. The absolute
// delta is still reported.
const TCPRetransRatioDenominatorFloor = 1000

// Derive computes one instant from a sample and its predecessor.
//
// A nil predecessor is not an error: the gauge-derived values need only the
// current sample, and the counter-derived ones are gaps. That split is why this
// function is worth having even when the panel has just started.
func Derive(previous *domain.NodeHostMetricSample, current *domain.NodeHostMetricSample) Derived {
	var derived Derived
	if current == nil {
		return derived
	}
	derived.applyGauges(current)
	if previous == nil || !differenceIsMeaningful(previous, current) {
		return derived
	}
	elapsed := current.ReceivedAt.Sub(previous.ReceivedAt)
	derived.applySystemCPU(previous, current)
	derived.applyCgroupCPU(previous, current, elapsed)
	// The primary figure is chosen AFTER both halves exist, because the choice is
	// a comparison between them; and the normalised load uses the same capacity
	// the CPU figure settled on, so the two cannot disagree about CPU count.
	derived.applyPrimaryCPU()
	derived.applyLoadNormalized(current)
	derived.applyProcesses(previous, current, elapsed)
	derived.applyTCP(previous, current)
	return derived
}

// applyGauges fills everything that needs one sample.
//
// These survive a missing predecessor, which matters: on the first sample after a
// panel restart the operator still wants to see how much memory and disk the node
// has, and deriving those from a counter pair is not what they are.
func (d *Derived) applyGauges(current *domain.NodeHostMetricSample) {
	d.applyMemory(current)
	d.applyFilesystem(current)
	if current.CoreRSSBytes != nil {
		d.CoreRSSBytes = current.CoreRSSBytes
	}
}

func (d *Derived) applyMemory(current *domain.NodeHostMetricSample) {
	// The cgroup's limit is preferred when there is one, because it is what the
	// container can actually reach: a 512 MiB container drawn against the host's
	// RAM reads as healthy right up to the moment it is killed.
	if current.CgroupMemoryCurrentBytes != nil && current.CgroupMemoryLimitBytes != nil && *current.CgroupMemoryLimitBytes > 0 {
		scope := MemoryScopeCgroup
		d.MemoryScope = &scope
		percent := 100 * float64(*current.CgroupMemoryCurrentBytes) / float64(*current.CgroupMemoryLimitBytes)
		d.CgroupMemoryUsedPercent = &percent
		d.MemoryUsedPercent = &percent
		return
	}
	if current.SystemMemoryTotalBytes != nil && *current.SystemMemoryTotalBytes > 0 && current.SystemMemoryAvailableBytes != nil {
		scope := MemoryScopeSystem
		d.MemoryScope = &scope
		used := *current.SystemMemoryTotalBytes - *current.SystemMemoryAvailableBytes
		percent := 100 * float64(used) / float64(*current.SystemMemoryTotalBytes)
		d.MemoryUsedBytes = &used
		d.MemoryUsedPercent = &percent
	}
}

func (d *Derived) applyFilesystem(current *domain.NodeHostMetricSample) {
	if current.FilesystemAvailableBytes != nil {
		available := *current.FilesystemAvailableBytes
		d.DiskAvailableBytes = &available
	}
	if current.FilesystemTotalBytes != nil && *current.FilesystemTotalBytes > 0 && current.FilesystemAvailableBytes != nil {
		percent := 100 * float64(*current.FilesystemTotalBytes-*current.FilesystemAvailableBytes) / float64(*current.FilesystemTotalBytes)
		d.DiskUsedPercent = &percent
	}
	if current.FilesystemTotalInodes != nil && *current.FilesystemTotalInodes > 0 && current.FilesystemAvailableInodes != nil {
		percent := 100 * float64(*current.FilesystemTotalInodes-*current.FilesystemAvailableInodes) / float64(*current.FilesystemTotalInodes)
		d.InodeUsedPercent = &percent
	}
}

func (d *Derived) applySystemCPU(previous, current *domain.NodeHostMetricSample) {
	if previous.SystemCPUTotal == nil || current.SystemCPUTotal == nil {
		return
	}
	deltaTotal := *current.SystemCPUTotal - *previous.SystemCPUTotal
	if deltaTotal == 0 {
		// Every field null, per §9.1: a zero denominator is a pair of samples that
		// describe no interval at all.
		return
	}
	deltaIdle := counterDelta(previous.SystemCPUIdle, current.SystemCPUIdle)
	deltaIOWait := counterDelta(previous.SystemCPUIOWait, current.SystemCPUIOWait)
	deltaSteal := counterDelta(previous.SystemCPUSteal, current.SystemCPUSteal)

	busy := 100 * float64(deltaTotal-deltaIdle-deltaIOWait) / float64(deltaTotal)
	clampPercent(&busy)
	d.SystemCPUPercent = &busy

	// A FIELD THAT IS ABSENT ON EITHER SIDE IS A GAP, AND A FIELD THAT IS ZERO IS
	// A ZERO. The two must not share a representation: "this kernel does not
	// account for steal" and "nothing was stolen" look identical on a chart once
	// both are drawn at zero, and only one of them is a measurement.
	if previous.SystemCPUIOWait != nil && current.SystemCPUIOWait != nil {
		iowait := 100 * float64(deltaIOWait) / float64(deltaTotal)
		clampPercent(&iowait)
		d.SystemIOWaitPercent = &iowait
	}
	if previous.SystemCPUSteal != nil && current.SystemCPUSteal != nil {
		steal := 100 * float64(deltaSteal) / float64(deltaTotal)
		clampPercent(&steal)
		d.SystemStealPercent = &steal
	}
}

// applyCgroupCPU fills the container figures and then chooses the primary.
//
// THE TIGHTER CONSTRAINT WINS, and this is the one place the spec is emphatic:
// quota and cpuset can both be finite, and picking by a fixed precedence would
// report a container as having room it does not have. The capacity is the
// smaller of whatever is known.
func (d *Derived) applyCgroupCPU(previous, current *domain.NodeHostMetricSample, elapsed time.Duration) {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		return
	}
	elapsedUS := seconds * 1_000_000

	quotaCores, hasQuota := cgroupQuotaCores(current)
	effectiveCPUs := current.CgroupCPUEffectiveCPUs

	// The cgroup's own usage covers the container whether or not it is limited.
	if previous.CgroupCPUUsageUS != nil && current.CgroupCPUUsageUS != nil {
		deltaUsage := *current.CgroupCPUUsageUS - *previous.CgroupCPUUsageUS
		coresPercent := 100 * float64(deltaUsage) / elapsedUS
		d.CgroupCPUCoresPercent = &coresPercent

		if hasQuota {
			quotaPercent := coresPercent / quotaCores
			d.CgroupCPUQuotaPercent = &quotaPercent
		}
	}

	capacity := cgroupCapacityCores(quotaCores, hasQuota, effectiveCPUs)
	if capacity != nil && *capacity > 0 {
		d.CapacityCores = capacity
		if d.CgroupCPUCoresPercent != nil {
			capacityPercent := *d.CgroupCPUCoresPercent / *capacity
			d.CgroupCPUCapacityPercent = &capacityPercent
		}
	}

	// Throttling is a ratio of periods with a floored denominator, exactly as
	// §9.1 writes it: max(delta(periods), 1). The floor is what makes an interval
	// with no periods in it a ratio of zero rather than a division by nothing —
	// and it is deliberately NOT a gap, because "no periods elapsed" and "no
	// throttling happened" are the same statement here.
	if previous.CgroupCPUNrPeriods != nil && current.CgroupCPUNrPeriods != nil &&
		previous.CgroupCPUNrThrottled != nil && current.CgroupCPUNrThrottled != nil {
		deltaPeriods := *current.CgroupCPUNrPeriods - *previous.CgroupCPUNrPeriods
		deltaThrottled := *current.CgroupCPUNrThrottled - *previous.CgroupCPUNrThrottled
		denominator := deltaPeriods
		if denominator < 1 {
			denominator = 1
		}
		percent := 100 * float64(deltaThrottled) / float64(denominator)
		d.CgroupCPUThrottledPeriodPercent = &percent
	}
}

// applyPrimaryCPU decides the single figure the overview shows.
//
// It runs after both halves so it can compare them, which is why it is separate
// from applyCgroupCPU.
func (d *Derived) applyPrimaryCPU() {
	if d.CgroupCPUCapacityPercent != nil {
		scope := CPUScopeCgroupQuota
		if d.CgroupCPUQuotaPercent == nil {
			scope = CPUScopeCgroupCPUSet
		}
		d.CPUPercent = d.CgroupCPUCapacityPercent
		d.CPUScope = &scope
		return
	}
	if d.SystemCPUPercent != nil {
		scope := CPUScopeSystem
		d.CPUPercent = d.SystemCPUPercent
		d.CPUScope = &scope
	}
}

// applyLoadNormalized divides the fifteen-minute load by the same capacity the
// CPU figure uses, so the two cannot disagree about how many CPUs there are.
func (d *Derived) applyLoadNormalized(current *domain.NodeHostMetricSample) {
	if current.Load15 == nil {
		return
	}
	capacity := d.CapacityCores
	if capacity == nil && current.LogicalCPUs > 0 {
		logical := float64(current.LogicalCPUs)
		capacity = &logical
	}
	if capacity == nil || *capacity <= 0 {
		return
	}
	normalized := *current.Load15 / *capacity
	d.LoadNormalized = &normalized
}

func (d *Derived) applyProcesses(previous, current *domain.NodeHostMetricSample, elapsed time.Duration) {
	d.CoreCPUPercent = processCPUPercent(
		previous.CoreCPUTime, current.CoreCPUTime,
		previous.CoreCPUUnitsPerSecond, current.CoreCPUUnitsPerSecond, elapsed)
	d.AgentCPUPercent = processCPUPercent(
		previous.AgentCPUTime, current.AgentCPUTime,
		previous.AgentCPUUnitsPerSecond, current.AgentCPUUnitsPerSecond, elapsed)
}

// processCPUPercent turns a pair of tick counters into cores of CPU.
//
// THE UNITS-PER-SECOND MUST MATCH ON BOTH SIDES. Two samples of the same process
// reported at different clock rates have no common denominator, and dividing
// them would produce a number that looks like CPU usage and is not — which is
// exactly the kind of plausible-looking wrong figure this package avoids
// everywhere else.
func processCPUPercent(previousTicks, currentTicks, previousUnits, currentUnits *uint64, elapsed time.Duration) *float64 {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		return nil
	}
	if previousTicks == nil || currentTicks == nil || previousUnits == nil || currentUnits == nil {
		return nil
	}
	if *previousUnits != *currentUnits || *currentUnits == 0 {
		return nil
	}
	secondsOfCPU := float64(*currentTicks-*previousTicks) / float64(*currentUnits)
	coresPercent := 100 * secondsOfCPU / seconds
	return &coresPercent
}

func (d *Derived) applyTCP(previous, current *domain.NodeHostMetricSample) {
	if previous.TCPRetransSegments == nil || current.TCPRetransSegments == nil ||
		previous.TCPOutSegments == nil || current.TCPOutSegments == nil {
		return
	}
	deltaRetrans := *current.TCPRetransSegments - *previous.TCPRetransSegments
	deltaOut := *current.TCPOutSegments - *previous.TCPOutSegments
	d.TCPRetransDelta = &deltaRetrans
	if deltaOut < TCPRetransRatioDenominatorFloor {
		// A ratio against a handful of segments is not a fact about the network.
		// The absolute delta above is still reported.
		return
	}
	ratio := 100 * float64(deltaRetrans) / float64(deltaOut)
	d.TCPRetransPercent = &ratio
}

// differenceIsMeaningful applies the four conditions a pair must satisfy.
//
// THEY ARE ALL ABOUT THE PAIR BEING A MEASUREMENT OF ONE INTERVAL: causally
// ordered, in the same counter namespace, not going backwards, and close enough
// together that the numerator and denominator describe the same period. Failing
// any of them produces a gap rather than a guess.
func differenceIsMeaningful(previous, current *domain.NodeHostMetricSample) bool {
	if !current.ReceivedAt.After(previous.ReceivedAt) {
		return false
	}
	if elapsed := current.ReceivedAt.Sub(previous.ReceivedAt); elapsed < minDeriveInterval || elapsed > maxDeriveInterval {
		return false
	}
	// A reboot invalidates every kernel counter at once. Comparing across one
	// would read the whole previous boot's usage as this interval's.
	if previous.BootID != current.BootID {
		return false
	}
	if !counterEpochsAgree(previous, current) {
		return false
	}
	return !counterWentBackwards(previous, current)
}

// counterEpochsAgree checks the epochs of the counter families that carry one.
//
// A process epoch that moved means the core was replaced, so its counters
// restart; the kernel families are keyed by the boot id above.
func counterEpochsAgree(previous, current *domain.NodeHostMetricSample) bool {
	if !sameEpoch(previous.CoreProcessEpoch, current.CoreProcessEpoch) {
		return false
	}
	if !sameEpoch(previous.AgentProcessEpoch, current.AgentProcessEpoch) {
		return false
	}
	if !sameEpoch(previous.SystemCPUEpoch, current.SystemCPUEpoch) {
		return false
	}
	if !sameEpoch(previous.TCPEpoch, current.TCPEpoch) {
		return false
	}
	if !sameEpoch(previous.CgroupCPUEpoch, current.CgroupCPUEpoch) {
		return false
	}
	if !sameEpoch(previous.CgroupMemoryEpoch, current.CgroupMemoryEpoch) {
		return false
	}
	return true
}

// sameEpoch compares two optional epochs, treating an absent one on either side
// as "not comparable" only when the other is present.
func sameEpoch(previous, current *string) bool {
	if previous == nil && current == nil {
		return true
	}
	if previous == nil || current == nil {
		return false
	}
	return *previous == *current
}

// counterWentBackwards reports whether any cumulative counter decreased.
//
// The check is deliberately blanket rather than per-formula: a replayed or
// reordered sample is not detectable field by field, and a pair where one family
// went backwards is a pair whose other differences cannot be trusted either.
func counterWentBackwards(previous, current *domain.NodeHostMetricSample) bool {
	for _, pair := range [][2]*uint64{
		{previous.SystemCPUTotal, current.SystemCPUTotal},
		{previous.SystemCPUIdle, current.SystemCPUIdle},
		{previous.SystemCPUIOWait, current.SystemCPUIOWait},
		{previous.SystemCPUSteal, current.SystemCPUSteal},
		{previous.CgroupCPUUsageUS, current.CgroupCPUUsageUS},
		{previous.CgroupCPUNrPeriods, current.CgroupCPUNrPeriods},
		{previous.CgroupCPUNrThrottled, current.CgroupCPUNrThrottled},
		{previous.CgroupCPUThrottledUS, current.CgroupCPUThrottledUS},
		{previous.TCPRetransSegments, current.TCPRetransSegments},
		{previous.TCPOutSegments, current.TCPOutSegments},
		{previous.CoreCPUTime, current.CoreCPUTime},
		{previous.AgentCPUTime, current.AgentCPUTime},
	} {
		if pair[0] != nil && pair[1] != nil && *pair[1] < *pair[0] {
			return true
		}
	}
	return false
}

func counterDelta(previous, current *uint64) uint64 {
	if previous == nil || current == nil {
		return 0
	}
	return *current - *previous
}

// cgroupQuotaCores reads the quota a cgroup is allowed, in whole CPUs.
func cgroupQuotaCores(sample *domain.NodeHostMetricSample) (float64, bool) {
	if sample.CgroupCPUQuotaUS == nil || sample.CgroupCPUPeriodUS == nil || *sample.CgroupCPUPeriodUS == 0 {
		return 0, false
	}
	return float64(*sample.CgroupCPUQuotaUS) / float64(*sample.CgroupCPUPeriodUS), true
}

// cgroupCapacityCores is the smaller of the constraints that are actually known.
//
// TAKING THE MINIMUM IS THE POINT: a container with a 2-CPU quota pinned to one
// CPU by its cpuset has a capacity of one, and reporting two would present it as
// half as busy as it is.
func cgroupCapacityCores(quotaCores float64, hasQuota bool, effectiveCPUs *uint64) *float64 {
	var capacity *float64
	if hasQuota && quotaCores > 0 {
		value := quotaCores
		capacity = &value
	}
	if effectiveCPUs != nil && *effectiveCPUs > 0 {
		value := float64(*effectiveCPUs)
		if capacity == nil || value < *capacity {
			capacity = &value
		}
	}
	return capacity
}

// clampPercent bounds a percentage to 0..100 for drawing.
//
// IT IS A DEFENCE AGAINST A READ RACE, NOT A CORRECTION. A single sample taken
// mid-tick can produce a figure a hair outside the range, and a chart axis that
// jumps to 100.4 looks like a bug in the chart. A PERSISTENT out-of-range value
// means the collector is wrong, and clamping it would hide exactly that.
func clampPercent(value *float64) {
	if value == nil || math.IsNaN(*value) {
		return
	}
	if *value < 0 {
		*value = 0
	}
	if *value > 100 {
		*value = 100
	}
}

// InterfaceDerived is one interface's rates.
type InterfaceDerived struct {
	RXBps                  *float64
	TXBps                  *float64
	LinkUtilizationPercent *float64
	// ErrorRatio counts errors and drops together against the packets that
	// could have produced them. A link losing a tenth of a percent of its packets
	// is telling the operator something a throughput line cannot.
	ErrorRatio *float64
}

// DeriveInterface computes one interface's rates from a pair of its own rows.
//
// THE EPOCH IS A PARAMETER BECAUSE IT IS NOT ON THE ROW. Spec §5.8 makes an
// interface's counter identity the triple (counter epoch, index, name), and
// §8.3's column dictionary provides no epoch column — the agent's network epoch
// IS the boot id, which the sample already carries. Passing it in keeps the rule
// in one place instead of leaving it to each caller to remember.
//
// All three parts must match: a renumbered interface, a renamed one, or a reboot
// is a different series, and continuing one across any of those joins two
// unrelated measurements into a line that looks continuous.
func DeriveInterface(previous, current domain.NodeInterfaceMetricSample, previousEpoch, currentEpoch string) InterfaceDerived {
	var derived InterfaceDerived
	if previousEpoch != currentEpoch ||
		previous.InterfaceIndex != current.InterfaceIndex ||
		previous.InterfaceName != current.InterfaceName {
		return derived
	}
	elapsed := current.ReceivedAt.Sub(previous.ReceivedAt).Seconds()
	if elapsed < minDeriveInterval.Seconds() || elapsed > maxDeriveInterval.Seconds() {
		return derived
	}
	if current.RXBytes < previous.RXBytes || current.TXBytes < previous.TXBytes ||
		current.RXPackets < previous.RXPackets || current.TXPackets < previous.TXPackets {
		return derived
	}
	// Bits, not bytes: the protocol carries byte counters and every link is
	// quoted in bits, so the conversion happens once, here, rather than in each
	// consumer that would otherwise get it wrong at a different place.
	rx := 8 * float64(current.RXBytes-previous.RXBytes) / elapsed
	tx := 8 * float64(current.TXBytes-previous.TXBytes) / elapsed
	derived.RXBps = &rx
	derived.TXBps = &tx

	if current.LinkSpeedMbps != nil && *current.LinkSpeedMbps > 0 {
		// FULL DUPLEX: the larger direction against the link rate, never the sum
		// of both, which would compare a two-way total against a one-way capacity
		// and report a saturated link at 50% utilisation.
		peak := math.Max(rx, tx)
		utilization := 100 * peak / (float64(*current.LinkSpeedMbps) * 1_000_000)
		derived.LinkUtilizationPercent = &utilization
	}
	deltaPackets := (current.RXPackets - previous.RXPackets) + (current.TXPackets - previous.TXPackets)
	if deltaPackets > 0 {
		deltaErrors := (current.RXErrors - previous.RXErrors) + (current.RXDropped - previous.RXDropped) +
			(current.TXErrors - previous.TXErrors) + (current.TXDropped - previous.TXDropped)
		ratio := float64(deltaErrors) / float64(deltaPackets)
		derived.ErrorRatio = &ratio
	}
	return derived
}

// HostThroughput is the server-wide throughput figure.
//
// IT IS NULL UNLESS BOTH SIDES AGREE ON WHICH INTERFACES CARRY IT. The default
// interfaces are what "the server's traffic" means, and a routing change between
// the two samples makes that set different — so summing across the change would
// add one link's counters to another's, and the total would be of nothing.
func HostThroughput(previousDefaults, currentDefaults []string, previous, current map[string]InterfaceDerived) *float64 {
	if len(currentDefaults) == 0 || !sameInterfaceSet(previousDefaults, currentDefaults) {
		return nil
	}
	total := 0.0
	for _, name := range currentDefaults {
		derived, exists := current[name]
		if !exists || derived.RXBps == nil || derived.TXBps == nil {
			return nil
		}
		total += math.Max(*derived.RXBps, *derived.TXBps)
	}
	return &total
}

func sameInterfaceSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, name := range left {
		counts[name]++
	}
	for _, name := range right {
		counts[name]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
