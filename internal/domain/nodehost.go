package domain

import "time"

// This file holds the node host-telemetry persistence model. The types mirror
// the frozen column dictionary in the implementation spec §8.3; a column added
// there is added here, and the two are not allowed to drift.
//
// NULLABLE IS THE DEFAULT, AND THAT IS THE POINT. A metric the agent could not
// read is stored as NULL, never as 0. A zero is a measurement, and once the two
// share a storage representation the panel cannot draw the difference between
// "this host is idle" and "this agent could not see" — which is the failure the
// whole feature is written against. Pointer fields carry that through the
// database, the API and the chart.
//
// COUNTERS ARE STORED SIGNED. The wire validates every unsigned counter against
// MaxInt64 so all three dialects can hold it in a BIGINT, and a negative value
// read back is therefore corruption rather than a large number — the repositories
// refuse it instead of reinterpreting it.

// NodeHostObservation is the most recent telemetry snapshot for one agent. It is
// stored as canonical JSON rather than exploded into columns: it exists for the
// detail view and for additive fields this build knows about, and it is
// deliberately NOT the source the charts read.
type NodeHostObservation struct {
	AgentID string
	// SampleID is the agent's per-collection identity. The panel is idempotent
	// on (agent_id, sample_id), so a retried POST is recognised rather than
	// stored twice.
	SampleID string
	// PayloadDigest is the SHA-256 of the canonical JSON. The same SampleID with
	// a different digest is an identity conflict, not a duplicate.
	PayloadDigest string
	CollectedAt   time.Time
	// ReceivedAt is when the panel accepted the report. It is the axis the
	// charts use, so a node's wall clock cannot change how long a condition
	// appears to have lasted.
	ReceivedAt    time.Time
	BootID        string
	ResourceScope string
	SnapshotJSON  []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NodeHostMetricSample is one minute-resolution history row.
//
// Every metric is a pointer because every one of them can be legitimately
// absent: the agent omits what it could not read, and a column that could not
// carry an absence would make the omission indistinguishable from a zero.
type NodeHostMetricSample struct {
	ID            int64
	AgentID       string
	SampleID      string
	PayloadDigest string
	CollectedAt   time.Time
	ReceivedAt    time.Time
	BootID        string

	Deployment          string
	ResourceScope       string
	CgroupVersion       int
	DataFilesystemScope string
	LogicalCPUs         int

	SystemCPUEpoch  *string
	SystemCPUTotal  *uint64
	SystemCPUIdle   *uint64
	SystemCPUIOWait *uint64
	SystemCPUSteal  *uint64

	CgroupCPUEpoch         *string
	CgroupCPUUsageUS       *uint64
	CgroupCPUUserUS        *uint64
	CgroupCPUSystemUS      *uint64
	CgroupCPUNrPeriods     *uint64
	CgroupCPUNrThrottled   *uint64
	CgroupCPUThrottledUS   *uint64
	CgroupCPUQuotaUS       *uint64
	CgroupCPUPeriodUS      *uint64
	CgroupCPUEffectiveCPUs *uint64

	Load1  *float64
	Load5  *float64
	Load15 *float64

	SystemMemoryTotalBytes     *uint64
	SystemMemoryAvailableBytes *uint64
	SystemSwapTotalBytes       *uint64
	SystemSwapFreeBytes        *uint64

	CgroupMemoryEpoch        *string
	CgroupMemoryCurrentBytes *uint64
	CgroupMemoryLimitBytes   *uint64
	CgroupSwapCurrentBytes   *uint64
	CgroupSwapLimitBytes     *uint64
	CgroupOOMEvents          *uint64
	CgroupOOMKillEvents      *uint64

	FilesystemTotalBytes      *uint64
	FilesystemAvailableBytes  *uint64
	FilesystemTotalInodes     *uint64
	FilesystemAvailableInodes *uint64
	FilesystemReadOnly        *bool

	TCPEpoch              *string
	TCPActiveOpens        *uint64
	TCPPassiveOpens       *uint64
	TCPAttemptFails       *uint64
	TCPEstabResets        *uint64
	TCPInSegments         *uint64
	TCPOutSegments        *uint64
	TCPRetransSegments    *uint64
	TCPCurrentEstablished *uint64

	SocketTCPInUse    *uint64
	SocketTCPOrphan   *uint64
	SocketTCPTimeWait *uint64
	SocketUDPInUse    *uint64
	ConntrackCurrent  *uint64
	ConntrackLimit    *uint64

	AgentProcessEpoch      *string
	AgentStartedAt         *time.Time
	AgentCPUTime           *uint64
	AgentCPUUnitsPerSecond *uint64
	AgentRSSBytes          *uint64
	AgentOpenFDs           *uint64
	AgentFDLimit           *uint64
	AgentThreads           *uint64

	CoreProcessEpoch      *string
	CoreStartedAt         *time.Time
	CoreCPUTime           *uint64
	CoreCPUUnitsPerSecond *uint64
	CoreRSSBytes          *uint64
	CoreOpenFDs           *uint64
	CoreFDLimit           *uint64
	CoreThreads           *uint64

	AgentRuntimeStartedAt   *time.Time
	CoreRestartCount        *uint64
	LastSyncSuccessAt       *time.Time
	LastSyncFailureAt       *time.Time
	ConsecutiveSyncFailures *uint64
	SyncSuccessCount        *uint64
	SyncFailureCount        *uint64
	LastRoundTripMS         *uint64
	LastRequestBytes        *uint64
	LastResponseBytes       *uint64

	CollectorDurationMS uint64
	CreatedAt           time.Time
}

// NodeInterfaceMetricSample is one interface's row within a sample.
//
// It lives in its own table because it is the only part of a sample that scales
// with the host's layout rather than with a fixed field set: keeping it inline
// would either cap the interface count in the schema or make every sample row
// carry its widest possible case.
type NodeInterfaceMetricSample struct {
	SampleID       string
	AgentID        string
	InterfaceIndex int
	InterfaceName  string
	IsDefaultIPv4  bool
	IsDefaultIPv6  bool
	MTU            int
	Up             bool
	LinkSpeedMbps  *uint64
	RXBytes        uint64
	RXPackets      uint64
	RXErrors       uint64
	RXDropped      uint64
	TXBytes        uint64
	TXPackets      uint64
	TXErrors       uint64
	TXDropped      uint64
	ReceivedAt     time.Time
}

// NodeHostMetricHourly is one UTC hour's aggregate.
//
// Averages and maxima are POINTERS because an hour with no usable interval has
// no average — and a zero average would draw a flat line through an outage.
// CoverageSeconds is how much of the hour the figures actually describe, so a
// partially covered hour is drawn as partial rather than as healthy.
type NodeHostMetricHourly struct {
	BucketStart     time.Time
	AgentID         string
	SampleCount     int
	CoverageSeconds int

	SystemCPUAverage    *float64
	SystemCPUMax        *float64
	SystemIOWaitAverage *float64
	SystemIOWaitMax     *float64
	SystemStealAverage  *float64
	SystemStealMax      *float64

	CgroupCPUUsageAverage           *float64
	CgroupCPUUsageMax               *float64
	CgroupCPUThrottledPeriodAverage *float64
	CgroupCPUThrottledPeriodMax     *float64
	CgroupCPUThrottledTimeSum       *uint64

	LoadNormalizedAverage *float64
	LoadNormalizedMax     *float64

	MemoryUsedAverage  *float64
	MemoryUsedMax      *float64
	MemoryAvailableMin *uint64

	CgroupMemoryUsedAverage *float64
	CgroupMemoryUsedMax     *float64
	CgroupOOMDelta          *uint64
	CgroupOOMKillDelta      *uint64

	FilesystemAvailableMin       *uint64
	FilesystemInodesAvailableMin *uint64

	NetworkRXBpsAverage    *float64
	NetworkRXBpsMax        *float64
	NetworkTXBpsAverage    *float64
	NetworkTXBpsMax        *float64
	LinkUtilizationAverage *float64
	LinkUtilizationMax     *float64
	NetworkErrorsDelta     *uint64
	NetworkDropsDelta      *uint64

	TCPRetransRatioAverage *float64
	TCPRetransRatioMax     *float64
	TCPRetransDelta        *uint64

	AgentCPUAverage *float64
	AgentCPUMax     *float64
	AgentRSSAverage *float64
	AgentRSSMax     *float64
	AgentFDMax      *uint64

	CoreCPUAverage   *float64
	CoreCPUMax       *float64
	CoreRSSAverage   *float64
	CoreRSSMax       *float64
	CoreFDMax        *uint64
	CoreRestartDelta *uint64

	ConntrackRatioAverage *float64
	ConntrackRatioMax     *float64

	SyncRTTAverage   *float64
	SyncRTTMax       *float64
	SyncFailureDelta *uint64
}

// NodeHostSummary is the per-agent rollup the server list needs, in one batch.
//
// It exists so the list can render a badge without loading a snapshot: the list
// is paginated and a per-row snapshot read is the N+1 the spec forbids.
//
// IT CARRIES NO DERIVED PERCENTAGE, deliberately. A CPU percentage is a rate
// between two samples and a memory percentage follows a scope-priority rule, so
// both belong to the derivation layer that owns those formulas. Computing them
// here would put half a formula in the persistence layer, where the other half
// could not reach it.
type NodeHostSummary struct {
	PanelID       int64
	AgentID       string
	SampleID      string
	ReceivedAt    time.Time
	ResourceScope string
	// Unavailable is how many sections this agent could not read. A count rather
	// than the token list, because the list view needs to know the snapshot is
	// incomplete, not which parts of it are.
	Unavailable int
}

// NodeHostPersistRequest is one write attempt.
//
// The latest snapshot is always written; the history row is written only when
// the throttle allows it, and the interfaces only alongside a history row. The
// whole thing is ONE transaction, because a latest row that disagrees with the
// history it came from is worse than a missing one.
type NodeHostPersistRequest struct {
	Observation *NodeHostObservation
	// Sample is nil when the throttle suppresses history for this report.
	Sample     *NodeHostMetricSample
	Interfaces []NodeInterfaceMetricSample
}

// NodeHostPersistResult reports what the write actually did, so the caller can
// log and count outcomes rather than inferring them.
type NodeHostPersistResult struct {
	LatestUpdated bool
	// HistoryInserted is false when the sample was throttled, duplicated, or
	// absent.
	HistoryInserted bool
	// Duplicate means this exact (agent_id, sample_id, payload_digest) was
	// already stored. It is a success, not an error: the agent retried a report
	// the panel already has.
	Duplicate bool
	// IdentityConflict means the sample id was seen with a different payload.
	// The new host subtree is discarded and the core sync still succeeds.
	IdentityConflict bool
}

// NodeHostPruneRequest bounds one cleanup batch.
//
// Retention is applied in batches rather than as one statement: a large
// installation's delete would otherwise hold a long write transaction, and SQLite
// serialises writes.
type NodeHostPruneRequest struct {
	RawBefore    time.Time
	HourlyBefore time.Time
	Limit        int
}

type NodeHostPruneResult struct {
	RawDeleted       int64
	InterfaceDeleted int64
	HourlyDeleted    int64
}
