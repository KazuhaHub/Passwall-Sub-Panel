package nodehealth

import (
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The stable alert codes (spec §10.2). They are API and notification-template
// surface, so a published code is never renamed — a changed meaning gets a new
// code rather than a new reading of the old one.
const (
	CodeMetricStale        = "node_metric_stale"
	CodeCPUSaturated       = "node_cpu_saturated"
	CodeCPUIOWaitHigh      = "node_cpu_iowait_high"
	CodeCPUStealHigh       = "node_cpu_steal_high"
	CodeCPUThrottled       = "node_cpu_throttled"
	CodeLoadHigh           = "node_load_high"
	CodeMemoryPressure     = "node_memory_pressure"
	CodeSwapPressure       = "node_swap_pressure"
	CodeCgroupOOM          = "node_cgroup_oom"
	CodeDiskSpaceLow       = "node_disk_space_low"
	CodeInodeLow           = "node_inode_low"
	CodeFilesystemReadOnly = "node_filesystem_read_only"
	CodeBandwidthSaturated = "node_bandwidth_saturated"
	CodeNetworkErrors      = "node_network_errors"
	CodeTCPRetransHigh     = "node_tcp_retrans_high"
	CodeConntrackPressure  = "node_conntrack_pressure"
	CodeFDPressure         = "node_fd_pressure"
	CodeCoreRestartLoop    = "node_core_restart_loop"
	CodeSyncDegraded       = "node_sync_degraded"
	CodeClockSkew          = "node_clock_skew"
	CodeCollectorSlow      = "node_collector_slow"
)

// The default thresholds, compiled rather than configurable in this version.
//
// THE SPEC DEFERS THE SETTINGS UNTIL THERE IS PRODUCTION OBSERVATION TO SET THEM
// FROM. Shipping twenty unmeasured knobs at once is how a fleet ends up with
// alerts nobody can explain, and every one of these numbers is a judgement that
// wants evidence behind it before an operator can be asked to tune it.
var rules = []rule{
	{
		code: CodeCPUSaturated, unit: "percent",
		value: func(p Point) *float64 { return p.Derived.CPUPercent },
		// A longer hold for the warning than the critical: a host pinned at 90%
		// for ten minutes is worth knowing about, and one pinned at 97% for five
		// is already losing work.
		warning:  condition{value: 90, hold: 10 * time.Minute},
		critical: condition{value: 97, hold: 5 * time.Minute},
		// The recovery is LOWER than the warning, and that gap is the whole point:
		// a threshold with no hysteresis chatters across its own boundary.
		recovery: condition{value: 80, hold: 10 * time.Minute},
	},
	{
		code: CodeCPUIOWaitHigh, unit: "percent",
		value:    func(p Point) *float64 { return p.Derived.SystemIOWaitPercent },
		warning:  condition{value: 20, hold: 10 * time.Minute},
		critical: condition{value: 40, hold: 5 * time.Minute},
		recovery: condition{value: 10, hold: 10 * time.Minute},
	},
	{
		code: CodeCPUStealHigh, unit: "percent",
		value:    func(p Point) *float64 { return p.Derived.SystemStealPercent },
		warning:  condition{value: 10, hold: 10 * time.Minute},
		critical: condition{value: 25, hold: 5 * time.Minute},
		recovery: condition{value: 5, hold: 10 * time.Minute},
	},
	{
		code: CodeCPUThrottled, unit: "percent",
		value:    func(p Point) *float64 { return p.Derived.CgroupCPUThrottledPeriodPercent },
		warning:  condition{value: 20, hold: 10 * time.Minute},
		critical: condition{value: 50, hold: 5 * time.Minute},
		recovery: condition{value: 10, hold: 10 * time.Minute},
	},
	{
		code: CodeLoadHigh, unit: "ratio",
		value:    func(p Point) *float64 { return p.Derived.LoadNormalized },
		warning:  condition{value: 1.5, hold: 15 * time.Minute},
		critical: condition{value: 3, hold: 10 * time.Minute},
		recovery: condition{value: 1, hold: 15 * time.Minute},
	},
	{
		code: CodeMemoryPressure, unit: "percent",
		value: func(p Point) *float64 {
			// AVAILABLE, not used: the rules read better in the direction the
			// operator thinks in, and "10% available" is the condition.
			if p.Derived.MemoryUsedPercent == nil {
				return nil
			}
			available := 100 - *p.Derived.MemoryUsedPercent
			return &available
		},
		lowerIsWorse: true,
		warning:      condition{value: 10, hold: 5 * time.Minute},
		critical:     condition{value: 5, hold: 2 * time.Minute},
		recovery:     condition{value: 15, hold: 10 * time.Minute},
	},
	{
		code: CodeSwapPressure, unit: "percent",
		value: func(p Point) *float64 { return swapUsedPercent(&p.Sample) },
		// The warning additionally requires the figure to be GROWING, because
		// half a swap file in steady use is a configuration and half a swap file
		// filling up is an incident. The critical does not: at 80% the machine is
		// already in trouble and the direction of travel is a detail.
		requireGrowth: true,
		warning:       condition{value: 50, hold: 10 * time.Minute},
		critical:      condition{value: 80, hold: 5 * time.Minute},
		recovery:      condition{value: 30, hold: 15 * time.Minute},
	},
	{
		code: CodeDiskSpaceLow, unit: "percent",
		value: func(p Point) *float64 {
			if p.Sample.FilesystemTotalBytes == nil || p.Sample.FilesystemAvailableBytes == nil ||
				*p.Sample.FilesystemTotalBytes == 0 {
				return nil
			}
			available := 100 * float64(*p.Sample.FilesystemAvailableBytes) / float64(*p.Sample.FilesystemTotalBytes)
			return &available
		},
		lowerIsWorse: true,
		// TWO SAMPLES RATHER THAN A DURATION, as the spec states it: on a small
		// disk the condition can appear and clear within one interval, and a
		// duration would either miss it or hold it long after it resolved.
		warning:  condition{value: 15, samples: 2},
		critical: condition{value: 5, samples: 2},
		recovery: condition{value: 20, hold: 10 * time.Minute},
	},
	{
		code: CodeInodeLow, unit: "percent",
		value: func(p Point) *float64 {
			if p.Sample.FilesystemTotalInodes == nil || p.Sample.FilesystemAvailableInodes == nil ||
				*p.Sample.FilesystemTotalInodes == 0 {
				return nil
			}
			available := 100 * float64(*p.Sample.FilesystemAvailableInodes) / float64(*p.Sample.FilesystemTotalInodes)
			return &available
		},
		lowerIsWorse: true,
		warning:      condition{value: 15, samples: 2},
		critical:     condition{value: 5, samples: 2},
		recovery:     condition{value: 20, hold: 10 * time.Minute},
	},
	{
		code: CodeFilesystemReadOnly, unit: "boolean",
		value: func(p Point) *float64 {
			if p.Sample.FilesystemReadOnly == nil {
				return nil
			}
			if *p.Sample.FilesystemReadOnly {
				return float64Ptr(1)
			}
			return float64Ptr(0)
		},
		// ONE CONFIRMED SAMPLE for the trigger, because a read-only data
		// directory is not a threshold condition — it is either true or it is
		// not, and waiting would only delay the alert.
		warning:  condition{value: 1, samples: 1},
		critical: condition{value: 1, samples: 1},
		recovery: condition{value: 1, samples: 2},
	},
	{
		code: CodeBandwidthSaturated, unit: "percent",
		value:    func(p Point) *float64 { return p.Derived.LinkUtilizationPercent },
		warning:  condition{value: 80, hold: 10 * time.Minute},
		critical: condition{value: 95, hold: 5 * time.Minute},
		recovery: condition{value: 60, hold: 10 * time.Minute},
	},
	{
		code: CodeNetworkErrors, unit: "ratio",
		// The ratio is a fraction, so the thresholds are too: 1% is 0.01.
		value: func(p Point) *float64 {
			if p.Derived.NetworkErrorRatio == nil {
				return nil
			}
			value := *p.Derived.NetworkErrorRatio
			return &value
		},
		warning:  condition{value: 0.01, hold: 5 * time.Minute},
		critical: condition{value: 0.05, hold: 5 * time.Minute},
		recovery: condition{value: 0.002, hold: 10 * time.Minute},
	},
	{
		code: CodeTCPRetransHigh, unit: "percent",
		value:    func(p Point) *float64 { return p.Derived.TCPRetransPercent },
		warning:  condition{value: 5, hold: 5 * time.Minute},
		critical: condition{value: 15, hold: 5 * time.Minute},
		recovery: condition{value: 2, hold: 10 * time.Minute},
	},
	{
		code: CodeConntrackPressure, unit: "percent",
		value: func(p Point) *float64 {
			if p.Sample.ConntrackCurrent == nil || p.Sample.ConntrackLimit == nil ||
				*p.Sample.ConntrackLimit == 0 {
				return nil
			}
			ratio := 100 * float64(*p.Sample.ConntrackCurrent) / float64(*p.Sample.ConntrackLimit)
			return &ratio
		},
		warning:  condition{value: 80, hold: 5 * time.Minute},
		critical: condition{value: 95, hold: 2 * time.Minute},
		recovery: condition{value: 70, hold: 10 * time.Minute},
	},
	{
		code: CodeFDPressure, unit: "percent",
		value: func(p Point) *float64 {
			// Whichever process is closer to its limit is the one that matters:
			// a core about to run out of descriptors is a proxy outage, and the
			// agent running out is a node going dark.
			highest := -1.0
			for _, pair := range [][2]*uint64{
				{p.Sample.AgentOpenFDs, p.Sample.AgentFDLimit},
				{p.Sample.CoreOpenFDs, p.Sample.CoreFDLimit},
			} {
				if pair[0] == nil || pair[1] == nil || *pair[1] == 0 {
					continue
				}
				percent := 100 * float64(*pair[0]) / float64(*pair[1])
				if percent > highest {
					highest = percent
				}
			}
			if highest < 0 {
				return nil
			}
			return &highest
		},
		warning:  condition{value: 80, hold: 5 * time.Minute},
		critical: condition{value: 95, hold: 2 * time.Minute},
		recovery: condition{value: 70, hold: 10 * time.Minute},
	},
	{
		code: CodeSyncDegraded, unit: "count",
		value: func(p Point) *float64 {
			if p.Sample.ConsecutiveSyncFailures == nil {
				return nil
			}
			value := float64(*p.Sample.ConsecutiveSyncFailures)
			return &value
		},
		// A COUNT IS ITS OWN HOLD: three consecutive failures already means three
		// rounds went wrong, and adding a duration would double-count the same
		// evidence.
		warning:  condition{value: 3, samples: 1},
		critical: condition{value: 10, samples: 1},
		recovery: condition{value: 0, samples: 1},
	},
	{
		code: CodeCollectorSlow, unit: "milliseconds",
		value: func(p Point) *float64 {
			value := float64(p.Sample.CollectorDurationMS)
			return &value
		},
		warning:  condition{value: 500, samples: 5},
		critical: condition{value: 1500, samples: 2},
		recovery: condition{value: 250, samples: 10},
	},
}

func float64Ptr(value float64) *float64 { return &value }

// swapUsedPercent reads the swap occupancy, preferring a finite cgroup limit.
//
// The cgroup's own pair is preferred for the same reason memory is: a container
// with a 512 MiB swap limit is not described by the host's swap figures.
func swapUsedPercent(sample *domain.NodeHostMetricSample) *float64 {
	if sample.CgroupSwapCurrentBytes != nil && sample.CgroupSwapLimitBytes != nil && *sample.CgroupSwapLimitBytes > 0 {
		percent := 100 * float64(*sample.CgroupSwapCurrentBytes) / float64(*sample.CgroupSwapLimitBytes)
		return &percent
	}
	if sample.SystemSwapTotalBytes != nil && *sample.SystemSwapTotalBytes > 0 && sample.SystemSwapFreeBytes != nil {
		used := *sample.SystemSwapTotalBytes - *sample.SystemSwapFreeBytes
		percent := 100 * float64(used) / float64(*sample.SystemSwapTotalBytes)
		return &percent
	}
	return nil
}
