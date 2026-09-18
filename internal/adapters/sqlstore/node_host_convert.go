package sqlstore

import (
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Row <-> domain conversions for node host telemetry.
//
// THE COUNTERS ARE CAST RATHER THAN RANGE-CHECKED ON THE WAY IN. The wire
// validated every unsigned counter against MaxInt64, and a caller that builds the
// domain directly with a larger value would wrap to a negative here — which the
// READ side refuses as corruption. The failure is therefore loud rather than
// silent, which is the property that matters; a per-field range check on the way
// in would be fifty more branches guarding a case the contract already excludes.
//
// Every nullable column maps to a pointer in both directions. A sample that
// omitted a section must round-trip as an omission, never as a zero.

func signedCounter(value *uint64) *int64 {
	if value == nil {
		return nil
	}
	converted := int64(*value)
	return &converted
}

func observationRowFromDomain(observation *domain.NodeHostObservation) *nodeHostObservationRow {
	return &nodeHostObservationRow{
		AgentID:       observation.AgentID,
		SampleID:      observation.SampleID,
		PayloadDigest: observation.PayloadDigest,
		CollectedAt:   observation.CollectedAt.UTC(),
		ReceivedAt:    observation.ReceivedAt.UTC(),
		BootID:        observation.BootID,
		ResourceScope: observation.ResourceScope,
		SnapshotJSON:  string(observation.SnapshotJSON),
		CPUPercent:    observation.CPUPercent, MemoryPercent: observation.MemoryPercent,
	}
}

func sampleRowFromDomain(sample *domain.NodeHostMetricSample) (*nodeHostMetricSampleRow, error) {
	return &nodeHostMetricSampleRow{
		AgentID:       sample.AgentID,
		SampleID:      sample.SampleID,
		PayloadDigest: sample.PayloadDigest,
		CollectedAt:   sample.CollectedAt.UTC(),
		ReceivedAt:    sample.ReceivedAt.UTC(),
		BootID:        sample.BootID,

		Deployment:          sample.Deployment,
		ResourceScope:       sample.ResourceScope,
		CgroupVersion:       sample.CgroupVersion,
		DataFilesystemScope: sample.DataFilesystemScope,
		LogicalCPUs:         sample.LogicalCPUs,

		SystemCPUEpoch:  sample.SystemCPUEpoch,
		SystemCPUTotal:  signedCounter(sample.SystemCPUTotal),
		SystemCPUIdle:   signedCounter(sample.SystemCPUIdle),
		SystemCPUIOWait: signedCounter(sample.SystemCPUIOWait),
		SystemCPUSteal:  signedCounter(sample.SystemCPUSteal),

		CgroupCPUEpoch:         sample.CgroupCPUEpoch,
		CgroupCPUUsageUS:       signedCounter(sample.CgroupCPUUsageUS),
		CgroupCPUUserUS:        signedCounter(sample.CgroupCPUUserUS),
		CgroupCPUSystemUS:      signedCounter(sample.CgroupCPUSystemUS),
		CgroupCPUNrPeriods:     signedCounter(sample.CgroupCPUNrPeriods),
		CgroupCPUNrThrottled:   signedCounter(sample.CgroupCPUNrThrottled),
		CgroupCPUThrottledUS:   signedCounter(sample.CgroupCPUThrottledUS),
		CgroupCPUQuotaUS:       signedCounter(sample.CgroupCPUQuotaUS),
		CgroupCPUPeriodUS:      signedCounter(sample.CgroupCPUPeriodUS),
		CgroupCPUEffectiveCPUs: signedCounter(sample.CgroupCPUEffectiveCPUs),

		Load1: sample.Load1, Load5: sample.Load5, Load15: sample.Load15,

		SystemMemoryTotalBytes:     signedCounter(sample.SystemMemoryTotalBytes),
		SystemMemoryAvailableBytes: signedCounter(sample.SystemMemoryAvailableBytes),
		SystemSwapTotalBytes:       signedCounter(sample.SystemSwapTotalBytes),
		SystemSwapFreeBytes:        signedCounter(sample.SystemSwapFreeBytes),

		CgroupMemoryEpoch:        sample.CgroupMemoryEpoch,
		CgroupMemoryCurrentBytes: signedCounter(sample.CgroupMemoryCurrentBytes),
		CgroupMemoryLimitBytes:   signedCounter(sample.CgroupMemoryLimitBytes),
		CgroupSwapCurrentBytes:   signedCounter(sample.CgroupSwapCurrentBytes),
		CgroupSwapLimitBytes:     signedCounter(sample.CgroupSwapLimitBytes),
		CgroupOOMEvents:          signedCounter(sample.CgroupOOMEvents),
		CgroupOOMKillEvents:      signedCounter(sample.CgroupOOMKillEvents),

		FilesystemTotalBytes:      signedCounter(sample.FilesystemTotalBytes),
		FilesystemAvailableBytes:  signedCounter(sample.FilesystemAvailableBytes),
		FilesystemTotalInodes:     signedCounter(sample.FilesystemTotalInodes),
		FilesystemAvailableInodes: signedCounter(sample.FilesystemAvailableInodes),
		FilesystemReadOnly:        sample.FilesystemReadOnly,

		TCPEpoch:              sample.TCPEpoch,
		TCPActiveOpens:        signedCounter(sample.TCPActiveOpens),
		TCPPassiveOpens:       signedCounter(sample.TCPPassiveOpens),
		TCPAttemptFails:       signedCounter(sample.TCPAttemptFails),
		TCPEstabResets:        signedCounter(sample.TCPEstabResets),
		TCPInSegments:         signedCounter(sample.TCPInSegments),
		TCPOutSegments:        signedCounter(sample.TCPOutSegments),
		TCPRetransSegments:    signedCounter(sample.TCPRetransSegments),
		TCPCurrentEstablished: signedCounter(sample.TCPCurrentEstablished),

		SocketTCPInUse:    signedCounter(sample.SocketTCPInUse),
		SocketTCPOrphan:   signedCounter(sample.SocketTCPOrphan),
		SocketTCPTimeWait: signedCounter(sample.SocketTCPTimeWait),
		SocketUDPInUse:    signedCounter(sample.SocketUDPInUse),
		ConntrackCurrent:  signedCounter(sample.ConntrackCurrent),
		ConntrackLimit:    signedCounter(sample.ConntrackLimit),

		AgentProcessEpoch:      sample.AgentProcessEpoch,
		AgentStartedAt:         utcOrNil(sample.AgentStartedAt),
		AgentCPUTime:           signedCounter(sample.AgentCPUTime),
		AgentCPUUnitsPerSecond: signedCounter(sample.AgentCPUUnitsPerSecond),
		AgentRSSBytes:          signedCounter(sample.AgentRSSBytes),
		AgentOpenFDs:           signedCounter(sample.AgentOpenFDs),
		AgentFDLimit:           signedCounter(sample.AgentFDLimit),
		AgentThreads:           signedCounter(sample.AgentThreads),

		CoreProcessEpoch:      sample.CoreProcessEpoch,
		CoreStartedAt:         utcOrNil(sample.CoreStartedAt),
		CoreCPUTime:           signedCounter(sample.CoreCPUTime),
		CoreCPUUnitsPerSecond: signedCounter(sample.CoreCPUUnitsPerSecond),
		CoreRSSBytes:          signedCounter(sample.CoreRSSBytes),
		CoreOpenFDs:           signedCounter(sample.CoreOpenFDs),
		CoreFDLimit:           signedCounter(sample.CoreFDLimit),
		CoreThreads:           signedCounter(sample.CoreThreads),

		AgentRuntimeStartedAt:   utcOrNil(sample.AgentRuntimeStartedAt),
		CoreRestartCount:        signedCounter(sample.CoreRestartCount),
		LastSyncSuccessAt:       utcOrNil(sample.LastSyncSuccessAt),
		LastSyncFailureAt:       utcOrNil(sample.LastSyncFailureAt),
		ConsecutiveSyncFailures: signedCounter(sample.ConsecutiveSyncFailures),
		SyncSuccessCount:        signedCounter(sample.SyncSuccessCount),
		SyncFailureCount:        signedCounter(sample.SyncFailureCount),
		LastRoundTripMS:         signedCounter(sample.LastRoundTripMS),
		LastRequestBytes:        signedCounter(sample.LastRequestBytes),
		LastResponseBytes:       signedCounter(sample.LastResponseBytes),

		CollectorDurationMS: int64(sample.CollectorDurationMS),
	}, nil
}

func (r *nodeHostMetricSampleRow) toDomain() (*domain.NodeHostMetricSample, error) {
	reader := &counterReader{}
	sample := &domain.NodeHostMetricSample{
		ID: r.ID, AgentID: r.AgentID, SampleID: r.SampleID, PayloadDigest: r.PayloadDigest,
		CollectedAt: r.CollectedAt.UTC(), ReceivedAt: r.ReceivedAt.UTC(), BootID: r.BootID,

		Deployment: r.Deployment, ResourceScope: r.ResourceScope,
		CgroupVersion: r.CgroupVersion, DataFilesystemScope: r.DataFilesystemScope,
		LogicalCPUs: r.LogicalCPUs,

		SystemCPUEpoch:  r.SystemCPUEpoch,
		SystemCPUTotal:  reader.take(r.SystemCPUTotal, "system_cpu_total"),
		SystemCPUIdle:   reader.take(r.SystemCPUIdle, "system_cpu_idle"),
		SystemCPUIOWait: reader.take(r.SystemCPUIOWait, "system_cpu_iowait"),
		SystemCPUSteal:  reader.take(r.SystemCPUSteal, "system_cpu_steal"),

		CgroupCPUEpoch:         r.CgroupCPUEpoch,
		CgroupCPUUsageUS:       reader.take(r.CgroupCPUUsageUS, "cgroup_cpu_usage_us"),
		CgroupCPUUserUS:        reader.take(r.CgroupCPUUserUS, "cgroup_cpu_user_us"),
		CgroupCPUSystemUS:      reader.take(r.CgroupCPUSystemUS, "cgroup_cpu_system_us"),
		CgroupCPUNrPeriods:     reader.take(r.CgroupCPUNrPeriods, "cgroup_cpu_nr_periods"),
		CgroupCPUNrThrottled:   reader.take(r.CgroupCPUNrThrottled, "cgroup_cpu_nr_throttled"),
		CgroupCPUThrottledUS:   reader.take(r.CgroupCPUThrottledUS, "cgroup_cpu_throttled_us"),
		CgroupCPUQuotaUS:       reader.take(r.CgroupCPUQuotaUS, "cgroup_cpu_quota_us"),
		CgroupCPUPeriodUS:      reader.take(r.CgroupCPUPeriodUS, "cgroup_cpu_period_us"),
		CgroupCPUEffectiveCPUs: reader.take(r.CgroupCPUEffectiveCPUs, "cgroup_cpu_effective_cpus"),

		Load1: r.Load1, Load5: r.Load5, Load15: r.Load15,

		SystemMemoryTotalBytes:     reader.take(r.SystemMemoryTotalBytes, "system_memory_total_bytes"),
		SystemMemoryAvailableBytes: reader.take(r.SystemMemoryAvailableBytes, "system_memory_available_bytes"),
		SystemSwapTotalBytes:       reader.take(r.SystemSwapTotalBytes, "system_swap_total_bytes"),
		SystemSwapFreeBytes:        reader.take(r.SystemSwapFreeBytes, "system_swap_free_bytes"),

		CgroupMemoryEpoch:        r.CgroupMemoryEpoch,
		CgroupMemoryCurrentBytes: reader.take(r.CgroupMemoryCurrentBytes, "cgroup_memory_current_bytes"),
		CgroupMemoryLimitBytes:   reader.take(r.CgroupMemoryLimitBytes, "cgroup_memory_limit_bytes"),
		CgroupSwapCurrentBytes:   reader.take(r.CgroupSwapCurrentBytes, "cgroup_swap_current_bytes"),
		CgroupSwapLimitBytes:     reader.take(r.CgroupSwapLimitBytes, "cgroup_swap_limit_bytes"),
		CgroupOOMEvents:          reader.take(r.CgroupOOMEvents, "cgroup_oom_events"),
		CgroupOOMKillEvents:      reader.take(r.CgroupOOMKillEvents, "cgroup_oom_kill_events"),

		FilesystemTotalBytes:      reader.take(r.FilesystemTotalBytes, "filesystem_total_bytes"),
		FilesystemAvailableBytes:  reader.take(r.FilesystemAvailableBytes, "filesystem_available_bytes"),
		FilesystemTotalInodes:     reader.take(r.FilesystemTotalInodes, "filesystem_total_inodes"),
		FilesystemAvailableInodes: reader.take(r.FilesystemAvailableInodes, "filesystem_available_inodes"),
		FilesystemReadOnly:        r.FilesystemReadOnly,

		TCPEpoch:              r.TCPEpoch,
		TCPActiveOpens:        reader.take(r.TCPActiveOpens, "tcp_active_opens"),
		TCPPassiveOpens:       reader.take(r.TCPPassiveOpens, "tcp_passive_opens"),
		TCPAttemptFails:       reader.take(r.TCPAttemptFails, "tcp_attempt_fails"),
		TCPEstabResets:        reader.take(r.TCPEstabResets, "tcp_estab_resets"),
		TCPInSegments:         reader.take(r.TCPInSegments, "tcp_in_segments"),
		TCPOutSegments:        reader.take(r.TCPOutSegments, "tcp_out_segments"),
		TCPRetransSegments:    reader.take(r.TCPRetransSegments, "tcp_retrans_segments"),
		TCPCurrentEstablished: reader.take(r.TCPCurrentEstablished, "tcp_current_established"),

		SocketTCPInUse:    reader.take(r.SocketTCPInUse, "socket_tcp_in_use"),
		SocketTCPOrphan:   reader.take(r.SocketTCPOrphan, "socket_tcp_orphan"),
		SocketTCPTimeWait: reader.take(r.SocketTCPTimeWait, "socket_tcp_time_wait"),
		SocketUDPInUse:    reader.take(r.SocketUDPInUse, "socket_udp_in_use"),
		ConntrackCurrent:  reader.take(r.ConntrackCurrent, "conntrack_current"),
		ConntrackLimit:    reader.take(r.ConntrackLimit, "conntrack_limit"),

		AgentProcessEpoch:      r.AgentProcessEpoch,
		AgentStartedAt:         utcOrNil(r.AgentStartedAt),
		AgentCPUTime:           reader.take(r.AgentCPUTime, "agent_cpu_time"),
		AgentCPUUnitsPerSecond: reader.take(r.AgentCPUUnitsPerSecond, "agent_cpu_units_per_second"),
		AgentRSSBytes:          reader.take(r.AgentRSSBytes, "agent_rss_bytes"),
		AgentOpenFDs:           reader.take(r.AgentOpenFDs, "agent_open_fds"),
		AgentFDLimit:           reader.take(r.AgentFDLimit, "agent_fd_limit"),
		AgentThreads:           reader.take(r.AgentThreads, "agent_threads"),

		CoreProcessEpoch:      r.CoreProcessEpoch,
		CoreStartedAt:         utcOrNil(r.CoreStartedAt),
		CoreCPUTime:           reader.take(r.CoreCPUTime, "core_cpu_time"),
		CoreCPUUnitsPerSecond: reader.take(r.CoreCPUUnitsPerSecond, "core_cpu_units_per_second"),
		CoreRSSBytes:          reader.take(r.CoreRSSBytes, "core_rss_bytes"),
		CoreOpenFDs:           reader.take(r.CoreOpenFDs, "core_open_fds"),
		CoreFDLimit:           reader.take(r.CoreFDLimit, "core_fd_limit"),
		CoreThreads:           reader.take(r.CoreThreads, "core_threads"),

		AgentRuntimeStartedAt:   utcOrNil(r.AgentRuntimeStartedAt),
		CoreRestartCount:        reader.take(r.CoreRestartCount, "core_restart_count"),
		LastSyncSuccessAt:       utcOrNil(r.LastSyncSuccessAt),
		LastSyncFailureAt:       utcOrNil(r.LastSyncFailureAt),
		ConsecutiveSyncFailures: reader.take(r.ConsecutiveSyncFailures, "consecutive_sync_failures"),
		SyncSuccessCount:        reader.take(r.SyncSuccessCount, "sync_success_count"),
		SyncFailureCount:        reader.take(r.SyncFailureCount, "sync_failure_count"),
		LastRoundTripMS:         reader.take(r.LastRoundTripMS, "last_round_trip_ms"),
		LastRequestBytes:        reader.take(r.LastRequestBytes, "last_request_bytes"),
		LastResponseBytes:       reader.take(r.LastResponseBytes, "last_response_bytes"),

		CollectorDurationMS: uint64(r.CollectorDurationMS),
		CreatedAt:           r.CreatedAt.UTC(),
	}
	if reader.err != nil {
		return nil, reader.err
	}
	return sample, nil
}

func interfaceRowFromDomain(sample *domain.NodeInterfaceMetricSample) nodeInterfaceMetricSampleRow {
	return nodeInterfaceMetricSampleRow{
		AgentID: sample.AgentID, SampleID: sample.SampleID,
		InterfaceIndex: sample.InterfaceIndex, InterfaceName: sample.InterfaceName,
		IsDefaultIPv4: sample.IsDefaultIPv4, IsDefaultIPv6: sample.IsDefaultIPv6,
		MTU: sample.MTU, Up: sample.Up,
		LinkSpeedMbps: signedCounter(sample.LinkSpeedMbps),
		RXBytes:       int64(sample.RXBytes), RXPackets: int64(sample.RXPackets),
		RXErrors: int64(sample.RXErrors), RXDropped: int64(sample.RXDropped),
		TXBytes: int64(sample.TXBytes), TXPackets: int64(sample.TXPackets),
		TXErrors: int64(sample.TXErrors), TXDropped: int64(sample.TXDropped),
		ReceivedAt: sample.ReceivedAt.UTC(),
	}
}

func (r *nodeInterfaceMetricSampleRow) toDomain() domain.NodeInterfaceMetricSample {
	return domain.NodeInterfaceMetricSample{
		AgentID: r.AgentID, SampleID: r.SampleID,
		InterfaceIndex: r.InterfaceIndex, InterfaceName: r.InterfaceName,
		IsDefaultIPv4: r.IsDefaultIPv4, IsDefaultIPv6: r.IsDefaultIPv6,
		MTU: r.MTU, Up: r.Up,
		LinkSpeedMbps: unsignedCounter(r.LinkSpeedMbps),
		RXBytes:       uint64(r.RXBytes), RXPackets: uint64(r.RXPackets),
		RXErrors: uint64(r.RXErrors), RXDropped: uint64(r.RXDropped),
		TXBytes: uint64(r.TXBytes), TXPackets: uint64(r.TXPackets),
		TXErrors: uint64(r.TXErrors), TXDropped: uint64(r.TXDropped),
		ReceivedAt: r.ReceivedAt.UTC(),
	}
}

func unsignedCounter(value *int64) *uint64 {
	if value == nil || *value < 0 {
		return nil
	}
	converted := uint64(*value)
	return &converted
}

func hourlyRowFromDomain(hourly *domain.NodeHostMetricHourly) nodeHostMetricHourlyRow {
	return nodeHostMetricHourlyRow{
		AgentID: hourly.AgentID, BucketStart: hourly.BucketStart.UTC(),
		SampleCount: hourly.SampleCount, CoverageSeconds: hourly.CoverageSeconds,

		SystemCPUAverage: hourly.SystemCPUAverage, SystemCPUMax: hourly.SystemCPUMax,
		SystemIOWaitAverage: hourly.SystemIOWaitAverage, SystemIOWaitMax: hourly.SystemIOWaitMax,
		SystemStealAverage: hourly.SystemStealAverage, SystemStealMax: hourly.SystemStealMax,

		CgroupCPUUsageAverage: hourly.CgroupCPUUsageAverage, CgroupCPUUsageMax: hourly.CgroupCPUUsageMax,
		CgroupCPUThrottledPeriodAverage: hourly.CgroupCPUThrottledPeriodAverage,
		CgroupCPUThrottledPeriodMax:     hourly.CgroupCPUThrottledPeriodMax,
		CgroupCPUThrottledTimeSum:       signedCounter(hourly.CgroupCPUThrottledTimeSum),

		LoadNormalizedAverage: hourly.LoadNormalizedAverage, LoadNormalizedMax: hourly.LoadNormalizedMax,

		MemoryUsedAverage: hourly.MemoryUsedAverage, MemoryUsedMax: hourly.MemoryUsedMax,
		MemoryAvailableMin: signedCounter(hourly.MemoryAvailableMin),

		CgroupMemoryUsedAverage: hourly.CgroupMemoryUsedAverage, CgroupMemoryUsedMax: hourly.CgroupMemoryUsedMax,
		CgroupOOMDelta: signedCounter(hourly.CgroupOOMDelta), CgroupOOMKillDelta: signedCounter(hourly.CgroupOOMKillDelta),

		FilesystemAvailableMin:       signedCounter(hourly.FilesystemAvailableMin),
		FilesystemInodesAvailableMin: signedCounter(hourly.FilesystemInodesAvailableMin),

		NetworkRXBpsAverage: hourly.NetworkRXBpsAverage, NetworkRXBpsMax: hourly.NetworkRXBpsMax,
		NetworkTXBpsAverage: hourly.NetworkTXBpsAverage, NetworkTXBpsMax: hourly.NetworkTXBpsMax,
		LinkUtilizationAverage: hourly.LinkUtilizationAverage, LinkUtilizationMax: hourly.LinkUtilizationMax,
		NetworkErrorsDelta: signedCounter(hourly.NetworkErrorsDelta), NetworkDropsDelta: signedCounter(hourly.NetworkDropsDelta),

		TCPRetransRatioAverage: hourly.TCPRetransRatioAverage, TCPRetransRatioMax: hourly.TCPRetransRatioMax,
		TCPRetransDelta: signedCounter(hourly.TCPRetransDelta),

		AgentCPUAverage: hourly.AgentCPUAverage, AgentCPUMax: hourly.AgentCPUMax,
		AgentRSSAverage: hourly.AgentRSSAverage, AgentRSSMax: hourly.AgentRSSMax,
		AgentFDMax: signedCounter(hourly.AgentFDMax),

		CoreCPUAverage: hourly.CoreCPUAverage, CoreCPUMax: hourly.CoreCPUMax,
		CoreRSSAverage: hourly.CoreRSSAverage, CoreRSSMax: hourly.CoreRSSMax,
		CoreFDMax: signedCounter(hourly.CoreFDMax), CoreRestartDelta: signedCounter(hourly.CoreRestartDelta),

		ConntrackRatioAverage: hourly.ConntrackRatioAverage, ConntrackRatioMax: hourly.ConntrackRatioMax,

		SyncRTTAverage: hourly.SyncRTTAverage, SyncRTTMax: hourly.SyncRTTMax,
		SyncFailureDelta: signedCounter(hourly.SyncFailureDelta),
	}
}

func (r *nodeHostMetricHourlyRow) toDomain() domain.NodeHostMetricHourly {
	return domain.NodeHostMetricHourly{
		BucketStart: r.BucketStart.UTC(), AgentID: r.AgentID,
		SampleCount: r.SampleCount, CoverageSeconds: r.CoverageSeconds,

		SystemCPUAverage: r.SystemCPUAverage, SystemCPUMax: r.SystemCPUMax,
		SystemIOWaitAverage: r.SystemIOWaitAverage, SystemIOWaitMax: r.SystemIOWaitMax,
		SystemStealAverage: r.SystemStealAverage, SystemStealMax: r.SystemStealMax,

		CgroupCPUUsageAverage: r.CgroupCPUUsageAverage, CgroupCPUUsageMax: r.CgroupCPUUsageMax,
		CgroupCPUThrottledPeriodAverage: r.CgroupCPUThrottledPeriodAverage,
		CgroupCPUThrottledPeriodMax:     r.CgroupCPUThrottledPeriodMax,
		CgroupCPUThrottledTimeSum:       unsignedCounter(r.CgroupCPUThrottledTimeSum),

		LoadNormalizedAverage: r.LoadNormalizedAverage, LoadNormalizedMax: r.LoadNormalizedMax,

		MemoryUsedAverage: r.MemoryUsedAverage, MemoryUsedMax: r.MemoryUsedMax,
		MemoryAvailableMin: unsignedCounter(r.MemoryAvailableMin),

		CgroupMemoryUsedAverage: r.CgroupMemoryUsedAverage, CgroupMemoryUsedMax: r.CgroupMemoryUsedMax,
		CgroupOOMDelta: unsignedCounter(r.CgroupOOMDelta), CgroupOOMKillDelta: unsignedCounter(r.CgroupOOMKillDelta),

		FilesystemAvailableMin:       unsignedCounter(r.FilesystemAvailableMin),
		FilesystemInodesAvailableMin: unsignedCounter(r.FilesystemInodesAvailableMin),

		NetworkRXBpsAverage: r.NetworkRXBpsAverage, NetworkRXBpsMax: r.NetworkRXBpsMax,
		NetworkTXBpsAverage: r.NetworkTXBpsAverage, NetworkTXBpsMax: r.NetworkTXBpsMax,
		LinkUtilizationAverage: r.LinkUtilizationAverage, LinkUtilizationMax: r.LinkUtilizationMax,
		NetworkErrorsDelta: unsignedCounter(r.NetworkErrorsDelta), NetworkDropsDelta: unsignedCounter(r.NetworkDropsDelta),

		TCPRetransRatioAverage: r.TCPRetransRatioAverage, TCPRetransRatioMax: r.TCPRetransRatioMax,
		TCPRetransDelta: unsignedCounter(r.TCPRetransDelta),

		AgentCPUAverage: r.AgentCPUAverage, AgentCPUMax: r.AgentCPUMax,
		AgentRSSAverage: r.AgentRSSAverage, AgentRSSMax: r.AgentRSSMax,
		AgentFDMax: unsignedCounter(r.AgentFDMax),

		CoreCPUAverage: r.CoreCPUAverage, CoreCPUMax: r.CoreCPUMax,
		CoreRSSAverage: r.CoreRSSAverage, CoreRSSMax: r.CoreRSSMax,
		CoreFDMax: unsignedCounter(r.CoreFDMax), CoreRestartDelta: unsignedCounter(r.CoreRestartDelta),

		ConntrackRatioAverage: r.ConntrackRatioAverage, ConntrackRatioMax: r.ConntrackRatioMax,

		SyncRTTAverage: r.SyncRTTAverage, SyncRTTMax: r.SyncRTTMax,
		SyncFailureDelta: unsignedCounter(r.SyncFailureDelta),
	}
}

func utcOrNil(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.UTC()
	return &converted
}
