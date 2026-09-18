package nodemetrics

import (
	"math"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// One table per formula in the spec's §9, plus one for the preconditions every
// difference depends on. The tests are tables rather than scenarios because the
// formulas are the contract: a scenario proves the feature works, a table proves
// each number is the number the spec names.

var deriveBase = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func u64(value uint64) *uint64   { return &value }
func f64(value float64) *float64 { return &value }
func str(value string) *string   { return &value }

// sampleAt builds a minimal sample whose only meaningful field is its instant,
// its boot id and its epochs — so a case that is about the preconditions does not
// have to construct counters it will not use.
func sampleAt(at time.Time) *domain.NodeHostMetricSample {
	return &domain.NodeHostMetricSample{
		SampleID: "0123456789abcdef0123456789abcdef", BootID: "boot-1",
		ResourceScope: "host", ReceivedAt: at, LogicalCPUs: 4,
	}
}

func closeTo(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s is nil, want %v", name, want)
	}
	if math.Abs(*got-want) > 0.0001 {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func mustBeNil(t *testing.T, name string, got *float64) {
	t.Helper()
	if got != nil {
		t.Fatalf("%s = %v, want a gap", name, *got)
	}
}

// §9.1: the three system CPU percentages.
func TestDeriveSystemCPU(t *testing.T) {
	cases := []struct {
		name        string
		deltaTotal  uint64
		deltaIdle   uint64
		deltaIOWait uint64
		deltaSteal  uint64
		wantCPU     *float64
		wantIOWait  *float64
		wantSteal   *float64
	}{
		{
			// 1000 of 2000 ticks busy, with 100 of them waiting on IO.
			name:        "half busy",
			deltaTotal:  2000,
			deltaIdle:   900,
			deltaIOWait: 100,
			deltaSteal:  0,
			wantCPU:     f64(50), wantIOWait: f64(5), wantSteal: f64(0),
		},
		{
			// A totally idle host: busy is zero, and that is a measurement rather
			// than a gap.
			name:        "idle",
			deltaTotal:  1000,
			deltaIdle:   1000,
			deltaIOWait: 0,
			deltaSteal:  0,
			wantCPU:     f64(0), wantIOWait: f64(0), wantSteal: f64(0),
		},
		{
			// A stolen-time host: the steal is reported separately and is NOT
			// folded into busy, or a VM would look busy while it is in fact
			// waiting for its hypervisor.
			name:        "stolen",
			deltaTotal:  1000,
			deltaIdle:   500,
			deltaIOWait: 0,
			deltaSteal:  200,
			wantCPU:     f64(50), wantIOWait: f64(0), wantSteal: f64(20),
		},
		{
			// A zero denominator means the pair describes no interval at all.
			// Every counter unchanged: the pair describes no interval at all.
			name:       "no ticks elapsed",
			deltaTotal: 0,
			deltaIdle:  0,
			wantCPU:    nil, wantIOWait: nil, wantSteal: nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			previous := sampleAt(deriveBase)
			previous.SystemCPUTotal, previous.SystemCPUIdle = u64(0), u64(0)
			previous.SystemCPUIOWait, previous.SystemCPUSteal = u64(0), u64(0)
			current := sampleAt(deriveBase.Add(time.Minute))
			current.SystemCPUTotal = u64(testCase.deltaTotal)
			current.SystemCPUIdle = u64(testCase.deltaIdle)
			current.SystemCPUIOWait = u64(testCase.deltaIOWait)
			current.SystemCPUSteal = u64(testCase.deltaSteal)
			derived := Derive(previous, current)
			assertOptional(t, "system cpu", derived.SystemCPUPercent, testCase.wantCPU)
			assertOptional(t, "iowait", derived.SystemIOWaitPercent, testCase.wantIOWait)
			assertOptional(t, "steal", derived.SystemStealPercent, testCase.wantSteal)
		})
	}
}

func assertOptional(t *testing.T, name string, got, want *float64) {
	t.Helper()
	if want == nil {
		mustBeNil(t, name, got)
		return
	}
	closeTo(t, name, got, *want)
}

// §9.1's cgroup half, including the rule that decides which constraint is real.
func TestDeriveCgroupCPU(t *testing.T) {
	cases := []struct {
		name                string
		quotaUS, periodUS   *uint64
		effectiveCPUs       *uint64
		numPeriods          uint64
		throttled           uint64
		usageDeltaUS        uint64
		wantCores           *float64
		wantQuota           *float64
		wantCapacity        *float64
		wantThrottledPeriod *float64
		wantPrimaryScope    string
	}{
		{
			// One full CPU for a minute against a two-CPU quota: half the quota.
			name:    "quota two cpus, one used",
			quotaUS: u64(200000), periodUS: u64(100000),
			usageDeltaUS: 60_000_000,
			wantCores:    f64(100), wantQuota: f64(50), wantCapacity: f64(50),
			wantThrottledPeriod: f64(0),
			wantPrimaryScope:    CPUScopeCgroupQuota,
		},
		{
			// THE MINIMUM WINS. A two-CPU quota pinned to one CPU has a capacity
			// of one, so one full CPU is 100% of it and not 50%.
			name:    "cpuset is the tighter constraint",
			quotaUS: u64(200000), periodUS: u64(100000), effectiveCPUs: u64(1),
			usageDeltaUS: 60_000_000,
			wantCores:    f64(100), wantQuota: f64(50), wantCapacity: f64(100),
			wantThrottledPeriod: f64(0),
			wantPrimaryScope:    CPUScopeCgroupQuota,
		},
		{
			// No quota at all, only a cpuset: the scope says so.
			name:          "cpuset only",
			effectiveCPUs: u64(2),
			usageDeltaUS:  60_000_000,
			wantCores:     f64(100), wantCapacity: f64(50),
			wantThrottledPeriod: f64(0),
			wantPrimaryScope:    CPUScopeCgroupCPUSet,
		},
		{
			// Throttling is a ratio of periods, and it is reported alongside the
			// usage rather than instead of it.
			name:    "throttled",
			quotaUS: u64(100000), periodUS: u64(100000),
			numPeriods: 600, throttled: 60, usageDeltaUS: 30_000_000,
			wantCores: f64(50), wantQuota: f64(50), wantCapacity: f64(50),
			wantThrottledPeriod: f64(10),
			wantPrimaryScope:    CPUScopeCgroupQuota,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			previous := sampleAt(deriveBase)
			previous.CgroupCPUUsageUS = u64(0)
			previous.CgroupCPUNrPeriods = u64(0)
			previous.CgroupCPUNrThrottled = u64(0)
			current := sampleAt(deriveBase.Add(time.Minute))
			current.CgroupCPUUsageUS = u64(testCase.usageDeltaUS)
			current.CgroupCPUNrPeriods = u64(testCase.numPeriods)
			current.CgroupCPUNrThrottled = u64(testCase.throttled)
			current.CgroupCPUQuotaUS = testCase.quotaUS
			current.CgroupCPUPeriodUS = testCase.periodUS
			current.CgroupCPUEffectiveCPUs = testCase.effectiveCPUs

			derived := Derive(previous, current)
			assertOptional(t, "cgroup cores", derived.CgroupCPUCoresPercent, testCase.wantCores)
			assertOptional(t, "cgroup quota", derived.CgroupCPUQuotaPercent, testCase.wantQuota)
			assertOptional(t, "cgroup capacity", derived.CgroupCPUCapacityPercent, testCase.wantCapacity)
			assertOptional(t, "throttled periods", derived.CgroupCPUThrottledPeriodPercent, testCase.wantThrottledPeriod)
			if derived.CPUScope == nil || *derived.CPUScope != testCase.wantPrimaryScope {
				t.Fatalf("primary scope = %v, want %q", derived.CPUScope, testCase.wantPrimaryScope)
			}
			// The primary figure is the capacity figure whenever there is one, so
			// the two cannot disagree about whether the container is busy.
			closeTo(t, "primary", derived.CPUPercent, *testCase.wantCapacity)
		})
	}
}

// §9.1's fallback: with no cgroup capacity at all the primary is the system
// figure and the scope says so.
func TestDerivePrimaryCPUFallsBackToSystem(t *testing.T) {
	previous := sampleAt(deriveBase)
	previous.SystemCPUTotal, previous.SystemCPUIdle = u64(0), u64(0)
	current := sampleAt(deriveBase.Add(time.Minute))
	current.SystemCPUTotal, current.SystemCPUIdle = u64(1000), u64(250)
	derived := Derive(previous, current)
	if derived.CPUScope == nil || *derived.CPUScope != CPUScopeSystem {
		t.Fatalf("scope = %v, want %q", derived.CPUScope, CPUScopeSystem)
	}
	closeTo(t, "primary", derived.CPUPercent, 75)
}

// §9.2: memory, with the cgroup preferred when it is limited.
func TestDeriveMemory(t *testing.T) {
	cases := []struct {
		name                         string
		systemTotal, systemAvailable *uint64
		cgroupCurrent, cgroupLimit   *uint64
		wantPercent                  *float64
		wantScope                    string
	}{
		{
			name:        "system only",
			systemTotal: u64(1000), systemAvailable: u64(250),
			wantPercent: f64(75), wantScope: MemoryScopeSystem,
		},
		{
			// THE CONTAINER'S LIMIT WINS. 100 MiB of a 512 MiB limit is 19.5%,
			// even though the host has gigabytes free — drawing it against the
			// host is how a container gets reported as healthy until it is killed.
			name:        "cgroup limit preferred",
			systemTotal: u64(16_000_000_000), systemAvailable: u64(15_000_000_000),
			cgroupCurrent: u64(100), cgroupLimit: u64(512),
			wantPercent: f64(19.53125), wantScope: MemoryScopeCgroup,
		},
		{
			// An unlimited cgroup has no percentage of its own, so the system
			// figures are what remain.
			name:        "cgroup with no limit falls back",
			systemTotal: u64(1000), systemAvailable: u64(500),
			cgroupCurrent: u64(400),
			wantPercent:   f64(50), wantScope: MemoryScopeSystem,
		},
		{
			name:        "neither readable",
			wantPercent: nil, wantScope: "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			current := sampleAt(deriveBase)
			current.SystemMemoryTotalBytes = testCase.systemTotal
			current.SystemMemoryAvailableBytes = testCase.systemAvailable
			current.CgroupMemoryCurrentBytes = testCase.cgroupCurrent
			current.CgroupMemoryLimitBytes = testCase.cgroupLimit
			derived := Derive(nil, current)
			assertOptional(t, "memory used", derived.MemoryUsedPercent, testCase.wantPercent)
			if testCase.wantScope == "" {
				if derived.MemoryScope != nil {
					t.Fatalf("scope = %q, want nil", *derived.MemoryScope)
				}
				return
			}
			if derived.MemoryScope == nil || *derived.MemoryScope != testCase.wantScope {
				t.Fatalf("scope = %v, want %q", derived.MemoryScope, testCase.wantScope)
			}
		})
	}
}

// §9.3: the data directory's filesystem.
func TestDeriveFilesystem(t *testing.T) {
	current := sampleAt(deriveBase)
	current.FilesystemTotalBytes = u64(1000)
	current.FilesystemAvailableBytes = u64(400)
	current.FilesystemTotalInodes = u64(2000)
	current.FilesystemAvailableInodes = u64(1500)
	derived := Derive(nil, current)
	closeTo(t, "disk used", derived.DiskUsedPercent, 60)
	closeTo(t, "inode used", derived.InodeUsedPercent, 25)

	// An inode count the filesystem does not report is a gap, not zero.
	current.FilesystemTotalInodes = nil
	current.FilesystemAvailableInodes = nil
	mustBeNil(t, "inode used", Derive(nil, current).InodeUsedPercent)
}

// §9.4: one interface's rates, including the full-duplex rule.
func TestDeriveInterface(t *testing.T) {
	base := domain.NodeInterfaceMetricSample{
		AgentID: "agt_1", SampleID: "a", InterfaceIndex: 2, InterfaceName: "eth0",
		MTU: 1500, Up: true, ReceivedAt: deriveBase,
	}
	current := base
	current.ReceivedAt = deriveBase.Add(time.Minute)
	current.RXBytes = 7_500_000  // 60 Mbit over a minute
	current.TXBytes = 15_000_000 // 120 Mbit
	current.RXPackets, current.TXPackets = 10_000, 20_000
	current.LinkSpeedMbps = u64(1000)

	derived := DeriveInterface(base, current, "boot-1", "boot-1")
	closeTo(t, "rx bps", derived.RXBps, 1_000_000)
	closeTo(t, "tx bps", derived.TXBps, 2_000_000)
	// FULL DUPLEX: the larger direction against the link rate. Summing both
	// would report a gigabit link as 0.3% utilised when it is carrying 0.2%.
	closeTo(t, "link utilization", derived.LinkUtilizationPercent, 0.2)

	// Errors against the packets that could have produced them.
	current.RXErrors, current.RXDropped = 100, 50
	derived = DeriveInterface(base, current, "boot-1", "boot-1")
	closeTo(t, "error ratio", derived.ErrorRatio, 150.0/30000.0)
}

// The three ways an interface pair stops being the same series.
func TestDeriveInterfaceBreaksOnIdentityChange(t *testing.T) {
	base := domain.NodeInterfaceMetricSample{
		AgentID: "agt_1", InterfaceIndex: 2, InterfaceName: "eth0", ReceivedAt: deriveBase,
	}
	current := base
	current.ReceivedAt = deriveBase.Add(time.Minute)
	current.RXBytes, current.TXBytes = 1000, 1000

	cases := []struct {
		name                    string
		previousEpoch, curEpoch string
		mutate                  func(*domain.NodeInterfaceMetricSample)
	}{
		{"a reboot", "boot-1", "boot-2", func(*domain.NodeInterfaceMetricSample) {}},
		{"a renumbered interface", "boot-1", "boot-1", func(s *domain.NodeInterfaceMetricSample) { s.InterfaceIndex = 3 }},
		{"a renamed interface", "boot-1", "boot-1", func(s *domain.NodeInterfaceMetricSample) { s.InterfaceName = "eth1" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := current
			testCase.mutate(&candidate)
			derived := DeriveInterface(base, candidate, testCase.previousEpoch, testCase.curEpoch)
			mustBeNil(t, "rx bps", derived.RXBps)
			mustBeNil(t, "tx bps", derived.TXBps)
		})
	}
}

// §9.4's server total: null unless both sides agree which interfaces carry it.
func TestHostThroughputRequiresTheSameDefaultSet(t *testing.T) {
	derived := map[string]InterfaceDerived{"eth0": {RXBps: f64(100), TXBps: f64(200)}}
	if total := HostThroughput([]string{"eth0"}, []string{"eth0"}, nil, derived); total == nil || *total != 200 {
		t.Fatalf("total = %v, want 200", total)
	}
	// The union of both default families, each counted once.
	both := map[string]InterfaceDerived{
		"eth0": {RXBps: f64(100), TXBps: f64(200)},
		"eth1": {RXBps: f64(50), TXBps: f64(50)},
	}
	if total := HostThroughput([]string{"eth0", "eth1"}, []string{"eth1", "eth0"}, nil, both); total == nil || *total != 250 {
		t.Fatalf("total = %v, want 250", total)
	}
	// A routing change between the samples makes the total meaningless.
	if total := HostThroughput([]string{"eth0"}, []string{"eth1"}, nil, both); total != nil {
		t.Fatalf("a routing change produced a total: %v", *total)
	}
	// And no default interface at all is not a total of zero.
	if total := HostThroughput(nil, nil, nil, both); total != nil {
		t.Fatalf("no default interfaces produced a total: %v", *total)
	}
}

// §9.5: the retransmission ratio and its denominator floor.
func TestDeriveTCPRetransmission(t *testing.T) {
	build := func(previousOut, currentOut, previousRetrans, currentRetrans uint64) Derived {
		previous := sampleAt(deriveBase)
		previous.TCPOutSegments, previous.TCPRetransSegments = u64(previousOut), u64(previousRetrans)
		current := sampleAt(deriveBase.Add(time.Minute))
		current.TCPOutSegments, current.TCPRetransSegments = u64(currentOut), u64(currentRetrans)
		return Derive(previous, current)
	}
	// 10 retransmissions out of 1000 segments.
	derived := build(0, 1000, 0, 10)
	closeTo(t, "retrans percent", derived.TCPRetransPercent, 1)

	// Below the floor the ratio is not evaluated, but the absolute delta is still
	// a fact and is reported.
	derived = build(0, 100, 0, 5)
	mustBeNil(t, "retrans percent", derived.TCPRetransPercent)
	if derived.TCPRetransDelta == nil || *derived.TCPRetransDelta != 5 {
		t.Fatalf("delta = %v, want 5", derived.TCPRetransDelta)
	}
}

// §9.6: process CPU, in cores, against the units the process reports in.
func TestDeriveProcessCPU(t *testing.T) {
	previous := sampleAt(deriveBase)
	previous.CoreCPUTime, previous.CoreCPUUnitsPerSecond = u64(0), u64(100)
	current := sampleAt(deriveBase.Add(time.Minute))
	// Sixty seconds of ticks at 100 Hz is one full core for a minute.
	current.CoreCPUTime, current.CoreCPUUnitsPerSecond = u64(6000), u64(100)
	derived := Derive(previous, current)
	closeTo(t, "core cpu", derived.CoreCPUPercent, 100)

	// A changed clock rate has no common denominator, so the pair is a gap.
	current.CoreCPUUnitsPerSecond = u64(250)
	mustBeNil(t, "core cpu", Derive(previous, current).CoreCPUPercent)
}

// THE PRECONDITIONS EVERY FORMULA DEPENDS ON. Each of these produces a gap
// rather than a number, because each of them means the two samples do not
// describe one interval.
func TestDeriveRequiresAMeaningfulPair(t *testing.T) {
	build := func(mutate func(previous, current *domain.NodeHostMetricSample)) (Derived, Derived) {
		previous := sampleAt(deriveBase)
		previous.SystemCPUTotal, previous.SystemCPUIdle = u64(0), u64(0)
		current := sampleAt(deriveBase.Add(time.Minute))
		current.SystemCPUTotal, current.SystemCPUIdle = u64(1000), u64(250)
		mutate(previous, current)
		return Derive(previous, current), Derive(previous, current)
	}

	// The baseline: without a mutation the formula produces a number, so the
	// cases below are failing for the reason they name and not because the
	// fixture was wrong.
	derived, _ := build(func(previous, current *domain.NodeHostMetricSample) {})
	closeTo(t, "baseline", derived.SystemCPUPercent, 75)

	cases := []struct {
		name   string
		mutate func(previous, current *domain.NodeHostMetricSample)
	}{
		{"time reversed", func(previous, current *domain.NodeHostMetricSample) {
			current.ReceivedAt = deriveBase.Add(-time.Minute)
		}},
		{"closer than the floor", func(previous, current *domain.NodeHostMetricSample) {
			current.ReceivedAt = deriveBase.Add(time.Second)
		}},
		{"further than the ceiling", func(previous, current *domain.NodeHostMetricSample) {
			current.ReceivedAt = deriveBase.Add(time.Hour)
		}},
		{"a reboot", func(previous, current *domain.NodeHostMetricSample) {
			current.BootID = "boot-2"
		}},
		{"a system counter epoch change", func(previous, current *domain.NodeHostMetricSample) {
			previous.SystemCPUEpoch, current.SystemCPUEpoch = str("a"), str("b")
		}},
		{"a core process replacement", func(previous, current *domain.NodeHostMetricSample) {
			previous.CoreProcessEpoch, current.CoreProcessEpoch = str("a"), str("b")
		}},
		{"a counter reset", func(previous, current *domain.NodeHostMetricSample) {
			previous.SystemCPUTotal, current.SystemCPUTotal = u64(5000), u64(1000)
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			derived, _ := build(testCase.mutate)
			mustBeNil(t, "system cpu", derived.SystemCPUPercent)
		})
	}
}

// The gauges survive a pair the counters cannot use, because they never needed
// the pair. An operator watching a node that just rebooted still wants to know
// how much disk is left.
func TestGaugesSurviveWithoutAUsablePair(t *testing.T) {
	previous := sampleAt(deriveBase)
	current := sampleAt(deriveBase.Add(time.Second))
	current.BootID = "boot-2"
	current.SystemMemoryTotalBytes = u64(1000)
	current.SystemMemoryAvailableBytes = u64(400)
	current.FilesystemTotalBytes = u64(1000)
	current.FilesystemAvailableBytes = u64(250)

	derived := Derive(previous, current)
	mustBeNil(t, "cpu", derived.SystemCPUPercent)
	closeTo(t, "memory", derived.MemoryUsedPercent, 60)
	closeTo(t, "disk", derived.DiskUsedPercent, 75)
}

// §9.7's capacity basis: the primary figure must be a percentage OF the container
// when the container is what is limited.
func TestPrimaryCPUUsesTheContainerCapacity(t *testing.T) {
	previous := sampleAt(deriveBase)
	previous.SystemCPUTotal, previous.SystemCPUIdle = u64(0), u64(0)
	previous.CgroupCPUUsageUS = u64(0)
	current := sampleAt(deriveBase.Add(time.Minute))
	// A quarter-busy host, but a container pinned to one CPU using all of it.
	current.SystemCPUTotal, current.SystemCPUIdle = u64(4000), u64(3000)
	current.CgroupCPUUsageUS = u64(60_000_000)
	current.CgroupCPUEffectiveCPUs = u64(1)

	derived := Derive(previous, current)
	closeTo(t, "system", derived.SystemCPUPercent, 25)
	closeTo(t, "primary", derived.CPUPercent, 100)
	if derived.CPUScope == nil || *derived.CPUScope != CPUScopeCgroupCPUSet {
		t.Fatalf("scope = %v, want the cpuset scope", derived.CPUScope)
	}
	// The normalised load uses the same capacity, so load and CPU cannot disagree
	// about how many CPUs there are.
	current.Load15 = f64(2)
	derived = Derive(previous, current)
	closeTo(t, "normalized load", derived.LoadNormalized, 2)
}
