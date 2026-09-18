package nodemetrics

import (
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Protocol -> domain conversion for the history tables.
//
// EVERY OPTIONAL FIELD STAYS OPTIONAL. The protocol carries an absent metric as
// a nil pointer and the domain stores it as NULL, so the two agree by
// construction: there is no branch here that could turn an absence into a zero,
// which is the conversion this feature most needs to not have.

// epochToTime converts a millisecond timestamp, treating zero as absent.
//
// Zero is only ever a legal value on the protocol's "has not happened yet"
// fields, so mapping it to nil is right for those and unreachable for the rest —
// and a wrong nil would show as a gap, where a wrong epoch would show as 1970.
func epochToTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	converted := time.UnixMilli(value).UTC()
	return &converted
}

func sampleFromObservation(agentID string, stored *domain.NodeHostObservation, observation *nodeprotocol.HostObservation) *domain.NodeHostMetricSample {
	sample := &domain.NodeHostMetricSample{
		AgentID: agentID, SampleID: observation.SampleID, PayloadDigest: stored.PayloadDigest,
		CollectedAt: stored.CollectedAt, ReceivedAt: stored.ReceivedAt, BootID: observation.BootID,

		Deployment:          string(observation.Scope.Deployment),
		ResourceScope:       string(observation.Scope.ResourceScope),
		CgroupVersion:       observation.Scope.CgroupVersion,
		DataFilesystemScope: string(observation.Scope.DataFilesystemScope),
		LogicalCPUs:         observation.Platform.LogicalCPUs,
	}

	if cpu := observation.CPU; cpu != nil {
		if system := cpu.System; system != nil {
			epoch := system.CounterEpoch
			sample.SystemCPUEpoch = &epoch
			sample.SystemCPUTotal = &system.Total
			sample.SystemCPUIdle = &system.Idle
			sample.SystemCPUIOWait = &system.IOWait
			sample.SystemCPUSteal = &system.Steal
		}
		if cgroup := cpu.Cgroup; cgroup != nil {
			if cgroup.CounterEpoch != "" {
				epoch := cgroup.CounterEpoch
				sample.CgroupCPUEpoch = &epoch
			}
			sample.CgroupCPUUsageUS = cgroup.UsageUS
			sample.CgroupCPUUserUS = cgroup.UserUS
			sample.CgroupCPUSystemUS = cgroup.SystemUS
			sample.CgroupCPUNrPeriods = cgroup.NrPeriods
			sample.CgroupCPUNrThrottled = cgroup.NrThrottled
			sample.CgroupCPUThrottledUS = cgroup.ThrottledUS
			sample.CgroupCPUQuotaUS = cgroup.QuotaUS
			sample.CgroupCPUPeriodUS = cgroup.PeriodUS
			sample.CgroupCPUEffectiveCPUs = cgroup.EffectiveCPUs
		}
	}
	if load := observation.Load; load != nil {
		sample.Load1 = &load.Load1
		sample.Load5 = &load.Load5
		sample.Load15 = &load.Load15
	}
	if memory := observation.Memory; memory != nil {
		if system := memory.System; system != nil {
			sample.SystemMemoryTotalBytes = &system.TotalBytes
			sample.SystemMemoryAvailableBytes = system.AvailableBytes
			sample.SystemSwapTotalBytes = &system.SwapTotalBytes
			sample.SystemSwapFreeBytes = &system.SwapFreeBytes
		}
		if cgroup := memory.Cgroup; cgroup != nil {
			epoch := cgroup.CounterEpoch
			sample.CgroupMemoryEpoch = &epoch
			current := cgroup.CurrentBytes
			sample.CgroupMemoryCurrentBytes = &current
			sample.CgroupMemoryLimitBytes = cgroup.LimitBytes
			sample.CgroupSwapCurrentBytes = cgroup.SwapCurrentBytes
			sample.CgroupSwapLimitBytes = cgroup.SwapLimitBytes
			sample.CgroupOOMEvents = cgroup.OOMEvents
			sample.CgroupOOMKillEvents = cgroup.OOMKillEvents
		}
	}
	if filesystem := observation.Filesystem; filesystem != nil {
		total := filesystem.TotalBytes
		available := filesystem.AvailableBytes
		readOnly := filesystem.ReadOnly
		sample.FilesystemTotalBytes = &total
		sample.FilesystemAvailableBytes = &available
		sample.FilesystemTotalInodes = filesystem.TotalInodes
		sample.FilesystemAvailableInodes = filesystem.AvailableInodes
		sample.FilesystemReadOnly = &readOnly
	}
	if tcp := observation.TCP; tcp != nil {
		epoch := tcp.CounterEpoch
		sample.TCPEpoch = &epoch
		sample.TCPActiveOpens = &tcp.ActiveOpens
		sample.TCPPassiveOpens = &tcp.PassiveOpens
		sample.TCPAttemptFails = &tcp.AttemptFails
		sample.TCPEstabResets = &tcp.EstabResets
		sample.TCPInSegments = &tcp.InSegments
		sample.TCPOutSegments = &tcp.OutSegments
		sample.TCPRetransSegments = &tcp.RetransSegments
		sample.TCPCurrentEstablished = &tcp.CurrentEstablished
	}
	if sockets := observation.Sockets; sockets != nil {
		sample.SocketTCPInUse = &sockets.TCPInUse
		sample.SocketTCPOrphan = &sockets.TCPOrphan
		sample.SocketTCPTimeWait = &sockets.TCPTimeWait
		sample.SocketUDPInUse = &sockets.UDPInUse
		sample.ConntrackCurrent = sockets.ConntrackCurrent
		sample.ConntrackLimit = sockets.ConntrackLimit
	}
	if processes := observation.Processes; processes != nil {
		applyProcessMetrics(sample, &processes.Agent, true)
		if processes.Core != nil {
			applyProcessMetrics(sample, processes.Core, false)
		}
	}
	if runtime := observation.Runtime; runtime != nil {
		sample.AgentRuntimeStartedAt = epochToTime(runtime.AgentStartedAtMS)
		restarts := runtime.CoreRestartCount
		sample.CoreRestartCount = &restarts
		sample.LastSyncSuccessAt = epochToTime(runtime.LastSyncSuccessAtMS)
		sample.LastSyncFailureAt = epochToTime(runtime.LastSyncFailureAtMS)
		failures := runtime.ConsecutiveSyncFailures
		successes := runtime.SyncSuccessCount
		syncFailures := runtime.SyncFailureCount
		sample.ConsecutiveSyncFailures = &failures
		sample.SyncSuccessCount = &successes
		sample.SyncFailureCount = &syncFailures
		sample.LastRoundTripMS = runtime.LastRoundTripMS
		sample.LastRequestBytes = runtime.LastRequestBytes
		sample.LastResponseBytes = runtime.LastResponseBytes
		sample.CollectorDurationMS = runtime.CollectorDurationMS
	}
	return sample
}

// applyProcessMetrics writes one process's columns.
//
// The two processes share a column shape and differ only in their prefix, so the
// choice is a parameter rather than two near-identical blocks that would drift.
func applyProcessMetrics(sample *domain.NodeHostMetricSample, metrics *nodeprotocol.ProcessMetrics, agent bool) {
	if agent {
		epoch := metrics.CounterEpoch
		sample.AgentProcessEpoch = &epoch
		sample.AgentStartedAt = epochToTime(metrics.StartedAtMS)
		sample.AgentCPUTime = metrics.CPUTime
		sample.AgentCPUUnitsPerSecond = metrics.CPUTimeUnitsPerSecond
		rss := metrics.RSSBytes
		openFDs := metrics.OpenFDs
		threads := metrics.Threads
		sample.AgentRSSBytes = &rss
		sample.AgentOpenFDs = &openFDs
		sample.AgentFDLimit = metrics.FDLimit
		sample.AgentThreads = &threads
		return
	}
	epoch := metrics.CounterEpoch
	sample.CoreProcessEpoch = &epoch
	sample.CoreStartedAt = epochToTime(metrics.StartedAtMS)
	sample.CoreCPUTime = metrics.CPUTime
	sample.CoreCPUUnitsPerSecond = metrics.CPUTimeUnitsPerSecond
	rss := metrics.RSSBytes
	openFDs := metrics.OpenFDs
	threads := metrics.Threads
	sample.CoreRSSBytes = &rss
	sample.CoreOpenFDs = &openFDs
	sample.CoreFDLimit = metrics.FDLimit
	sample.CoreThreads = &threads
}

func interfacesFromObservation(agentID, sampleID string, receivedAt time.Time, observation *nodeprotocol.HostObservation) []domain.NodeInterfaceMetricSample {
	if observation.Network == nil || len(observation.Network.Interfaces) == 0 {
		return nil
	}
	interfaces := make([]domain.NodeInterfaceMetricSample, 0, len(observation.Network.Interfaces))
	for _, source := range observation.Network.Interfaces {
		interfaces = append(interfaces, domain.NodeInterfaceMetricSample{
			AgentID: agentID, SampleID: sampleID,
			InterfaceIndex: source.Index, InterfaceName: source.Name,
			IsDefaultIPv4: source.Name == observation.Network.DefaultIPv4Interface,
			IsDefaultIPv6: source.Name == observation.Network.DefaultIPv6Interface,
			MTU:           source.MTU, Up: source.Up,
			LinkSpeedMbps: source.LinkSpeedMbps,
			RXBytes:       source.RXBytes, RXPackets: source.RXPackets,
			RXErrors: source.RXErrors, RXDropped: source.RXDropped,
			TXBytes: source.TXBytes, TXPackets: source.TXPackets,
			TXErrors: source.TXErrors, TXDropped: source.TXDropped,
			ReceivedAt: receivedAt.UTC(),
		})
	}
	return interfaces
}
