package sqlstore

import (
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Node host-telemetry tables.
//
// FOUR TABLES BECAUSE THEY HAVE FOUR DIFFERENT SHAPES. The latest snapshot is
// one row per agent and is read whole; the minute history is append-only and read
// by range; the interfaces scale with the host's layout rather than with a fixed
// field set; and the hourly aggregate is upserted rather than appended. Putting
// any two of them together would force one of them to be stored in a shape that
// does not match how it is read.
//
// COUNTERS ARE SIGNED COLUMNS. The wire validates every unsigned counter against
// MaxInt64 precisely so all three dialects can hold it in a BIGINT, so a negative
// value read back is corruption rather than a large number — the repository
// refuses it instead of reinterpreting it as 2^64-x.
//
// THE SNAPSHOT IS TEXT, following this package's convention for JSON-shaped
// data. A []byte column would map to bytea on PostgreSQL and BLOB on MySQL, and
// the two disagree about how the driver hands the value back.

type nodeHostObservationRow struct {
	AgentID       string    `gorm:"primaryKey;size:64"`
	SampleID      string    `gorm:"not null;size:32"`
	PayloadDigest string    `gorm:"not null;size:64"`
	CollectedAt   time.Time `gorm:"not null"`
	ReceivedAt    time.Time `gorm:"not null"`
	BootID        string    `gorm:"not null;size:64;default:''"`
	ResourceScope string    `gorm:"not null;size:16"`
	// SnapshotJSON is the canonical re-encoded observation. It is capped by the
	// service before it reaches here; the column is text so the three dialects
	// agree on how it comes back.
	SnapshotJSON string `gorm:"not null;type:text"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (nodeHostObservationRow) TableName() string { return "node_host_observations" }

func (r *nodeHostObservationRow) toDomain() *domain.NodeHostObservation {
	return &domain.NodeHostObservation{
		AgentID: r.AgentID, SampleID: r.SampleID, PayloadDigest: r.PayloadDigest,
		CollectedAt: r.CollectedAt.UTC(), ReceivedAt: r.ReceivedAt.UTC(),
		BootID: r.BootID, ResourceScope: r.ResourceScope,
		SnapshotJSON: []byte(r.SnapshotJSON),
		CreatedAt:    r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// nodeHostMetricSampleRow is the minute-resolution history.
//
// EVERY METRIC COLUMN IS NULLABLE, including the ones the agent always sends.
// The alternative is a row that cannot represent "this sample did not carry that
// section", which is exactly the distinction the feature exists to preserve.
type nodeHostMetricSampleRow struct {
	ID            int64     `gorm:"primaryKey;autoIncrement"`
	AgentID       string    `gorm:"not null;size:64;uniqueIndex:idx_node_host_sample,priority:1;index:idx_node_host_received,priority:1"`
	SampleID      string    `gorm:"not null;size:32;uniqueIndex:idx_node_host_sample,priority:2"`
	PayloadDigest string    `gorm:"not null;size:64"`
	CollectedAt   time.Time `gorm:"not null"`
	ReceivedAt    time.Time `gorm:"not null;index:idx_node_host_received,priority:2"`
	BootID        string    `gorm:"not null;size:64;default:''"`

	Deployment          string `gorm:"not null;size:16"`
	ResourceScope       string `gorm:"not null;size:16"`
	CgroupVersion       int    `gorm:"not null"`
	DataFilesystemScope string `gorm:"not null;size:16"`
	LogicalCPUs         int    `gorm:"not null"`

	SystemCPUEpoch  *string
	SystemCPUTotal  *int64
	SystemCPUIdle   *int64
	SystemCPUIOWait *int64
	SystemCPUSteal  *int64

	CgroupCPUEpoch         *string
	CgroupCPUUsageUS       *int64
	CgroupCPUUserUS        *int64
	CgroupCPUSystemUS      *int64
	CgroupCPUNrPeriods     *int64
	CgroupCPUNrThrottled   *int64
	CgroupCPUThrottledUS   *int64
	CgroupCPUQuotaUS       *int64
	CgroupCPUPeriodUS      *int64
	CgroupCPUEffectiveCPUs *int64

	Load1  *float64
	Load5  *float64
	Load15 *float64

	SystemMemoryTotalBytes     *int64
	SystemMemoryAvailableBytes *int64
	SystemSwapTotalBytes       *int64
	SystemSwapFreeBytes        *int64

	CgroupMemoryEpoch        *string
	CgroupMemoryCurrentBytes *int64
	CgroupMemoryLimitBytes   *int64
	CgroupSwapCurrentBytes   *int64
	CgroupSwapLimitBytes     *int64
	CgroupOOMEvents          *int64
	CgroupOOMKillEvents      *int64

	FilesystemTotalBytes      *int64
	FilesystemAvailableBytes  *int64
	FilesystemTotalInodes     *int64
	FilesystemAvailableInodes *int64
	FilesystemReadOnly        *bool

	TCPEpoch              *string
	TCPActiveOpens        *int64
	TCPPassiveOpens       *int64
	TCPAttemptFails       *int64
	TCPEstabResets        *int64
	TCPInSegments         *int64
	TCPOutSegments        *int64
	TCPRetransSegments    *int64
	TCPCurrentEstablished *int64

	SocketTCPInUse    *int64
	SocketTCPOrphan   *int64
	SocketTCPTimeWait *int64
	SocketUDPInUse    *int64
	ConntrackCurrent  *int64
	ConntrackLimit    *int64

	AgentProcessEpoch      *string
	AgentStartedAt         *time.Time
	AgentCPUTime           *int64
	AgentCPUUnitsPerSecond *int64
	AgentRSSBytes          *int64
	AgentOpenFDs           *int64
	AgentFDLimit           *int64
	AgentThreads           *int64

	CoreProcessEpoch      *string
	CoreStartedAt         *time.Time
	CoreCPUTime           *int64
	CoreCPUUnitsPerSecond *int64
	CoreRSSBytes          *int64
	CoreOpenFDs           *int64
	CoreFDLimit           *int64
	CoreThreads           *int64

	AgentRuntimeStartedAt   *time.Time
	CoreRestartCount        *int64
	LastSyncSuccessAt       *time.Time
	LastSyncFailureAt       *time.Time
	ConsecutiveSyncFailures *int64
	SyncSuccessCount        *int64
	SyncFailureCount        *int64
	LastRoundTripMS         *int64
	LastRequestBytes        *int64
	LastResponseBytes       *int64

	CollectorDurationMS int64 `gorm:"not null"`
	CreatedAt           time.Time
}

func (nodeHostMetricSampleRow) TableName() string { return "node_host_metric_samples" }

// nodeInterfaceMetricSampleRow is one interface within one sample.
//
// The unique key is (sample_id, agent_id, interface_index) rather than a
// surrogate: an interface's identity IS the sample it belongs to plus its index,
// and that key is what makes a retried insert a no-op instead of a duplicate row.
type nodeInterfaceMetricSampleRow struct {
	AgentID        string `gorm:"primaryKey;size:64"`
	SampleID       string `gorm:"primaryKey;size:32"`
	InterfaceIndex int    `gorm:"primaryKey"`
	InterfaceName  string `gorm:"not null;size:64"`
	IsDefaultIPv4  bool   `gorm:"not null"`
	IsDefaultIPv6  bool   `gorm:"not null"`
	MTU            int    `gorm:"not null"`
	Up             bool   `gorm:"not null"`
	LinkSpeedMbps  *int64
	RXBytes        int64     `gorm:"not null"`
	RXPackets      int64     `gorm:"not null"`
	RXErrors       int64     `gorm:"not null"`
	RXDropped      int64     `gorm:"not null"`
	TXBytes        int64     `gorm:"not null"`
	TXPackets      int64     `gorm:"not null"`
	TXErrors       int64     `gorm:"not null"`
	TXDropped      int64     `gorm:"not null"`
	ReceivedAt     time.Time `gorm:"not null;index:idx_node_iface_received,priority:1"`
}

func (nodeInterfaceMetricSampleRow) TableName() string { return "node_interface_metric_samples" }

// nodeHostMetricHourlyRow is one UTC hour's aggregate.
//
// The identity is (agent_id, bucket_start), which is what makes the rollup
// idempotent: recomputing a bucket after a crash overwrites the same row rather
// than appending a second one, so the aggregate is safe to re-run before a prune
// and byte-identical when it is.
//
// A surrogate primary key sits in front of that uniqueness rather than behind it,
// so the batched retention delete can select rows by id — the three dialects
// disagree about whether DELETE ... LIMIT exists, and a composite key would force
// a per-dialect delete.
type nodeHostMetricHourlyRow struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`
	AgentID         string    `gorm:"not null;size:64;uniqueIndex:idx_node_host_hourly,priority:1"`
	BucketStart     time.Time `gorm:"not null;uniqueIndex:idx_node_host_hourly,priority:2"`
	SampleCount     int       `gorm:"not null"`
	CoverageSeconds int       `gorm:"not null"`

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
	CgroupCPUThrottledTimeSum       *int64

	LoadNormalizedAverage *float64
	LoadNormalizedMax     *float64

	MemoryUsedAverage  *float64
	MemoryUsedMax      *float64
	MemoryAvailableMin *int64

	CgroupMemoryUsedAverage *float64
	CgroupMemoryUsedMax     *float64
	CgroupOOMDelta          *int64
	CgroupOOMKillDelta      *int64

	FilesystemAvailableMin       *int64
	FilesystemInodesAvailableMin *int64

	NetworkRXBpsAverage    *float64
	NetworkRXBpsMax        *float64
	NetworkTXBpsAverage    *float64
	NetworkTXBpsMax        *float64
	LinkUtilizationAverage *float64
	LinkUtilizationMax     *float64
	NetworkErrorsDelta     *int64
	NetworkDropsDelta      *int64

	TCPRetransRatioAverage *float64
	TCPRetransRatioMax     *float64
	TCPRetransDelta        *int64

	AgentCPUAverage *float64
	AgentCPUMax     *float64
	AgentRSSAverage *float64
	AgentRSSMax     *float64
	AgentFDMax      *int64

	CoreCPUAverage   *float64
	CoreCPUMax       *float64
	CoreRSSAverage   *float64
	CoreRSSMax       *float64
	CoreFDMax        *int64
	CoreRestartDelta *int64

	ConntrackRatioAverage *float64
	ConntrackRatioMax     *float64

	SyncRTTAverage   *float64
	SyncRTTMax       *float64
	SyncFailureDelta *int64
}

func (nodeHostMetricHourlyRow) TableName() string { return "node_host_metric_hourly" }
