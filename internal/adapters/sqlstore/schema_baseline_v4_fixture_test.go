package sqlstore

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

// Frozen source: v4.0.0-beta.1, commit
// c6ca7d107a15419a4b179d8bc4e17695ab8012df:
// schema.go (nodeRow); psp_client_repo.go (client/attachment);
// node_agent_repo.go (agent/stream); node_agent_issue_repo.go (issue);
// node_agent_task_repo.go (task); node_agent_task_quarantine.go (quarantine).
// Only names change; types/tags are verbatim and prose comments are omitted.
// The users/group/panel/separator/migration models are unchanged from beta20
// and reuse the frozen models there. No current EnsureSchema builds this source.
type v400Beta1NodeRow struct {
	ID                      int64  `gorm:"primaryKey;autoIncrement"`
	PanelID                 int64  `gorm:"not null;index;uniqueIndex:uk_panel_inbound,priority:1"`
	InboundID               int    `gorm:"not null;uniqueIndex:uk_panel_inbound,priority:2"`
	DisplayName             string `gorm:"size:255;not null"`
	ServerAddress           string `gorm:"size:255"`
	Flow                    string `gorm:"size:64"`
	DesiredProtocol         string `gorm:"size:32;default:''"`
	DesiredPort             int    `gorm:"default:0"`
	ObservedProtocol        string `gorm:"size:32;default:''"`
	ObservedPort            int    `gorm:"default:0"`
	Region                  string `gorm:"size:16;not null"`
	Tags                    jsonStrings
	SortOrder               int    `gorm:"default:0"`
	Enabled                 *bool  `gorm:"default:true"`
	Kind                    string `gorm:"size:16;default:'real'"`
	LifetimeUpBytes         int64  `gorm:"default:0"`
	LifetimeDownBytes       int64  `gorm:"default:0"`
	LifetimeTotalBytes      int64  `gorm:"default:0"`
	LastTrafficUpBytes      int64  `gorm:"default:0"`
	LastTrafficDownBytes    int64  `gorm:"default:0"`
	LastTrafficTotalBytes   int64  `gorm:"default:0"`
	LastInboundUpBytes      int64  `gorm:"default:0"`
	LastInboundDownBytes    int64  `gorm:"default:0"`
	LastInboundTotalBytes   int64  `gorm:"default:0"`
	LastInboundCounterEpoch uint64 `gorm:"default:0"`
	LastInboundSeeded       bool   `gorm:"default:false"`
	HealthState             string `gorm:"size:32;default:''"`
	HealthCheckedAt         *time.Time
	HealthDetail            string `gorm:"size:512;default:''"`
	InboundListen           string `gorm:"size:64;default:''"`
	InboundRemark           string `gorm:"size:255;default:''"`
	InboundSettings         string `gorm:"type:text"`
	StreamSettings          string `gorm:"type:text"`
	Sniffing                string `gorm:"type:text"`
	Allocate                string `gorm:"type:text"`
	InboundExpiryTime       int64  `gorm:"default:0"`
	ConfigSyncedAt          *time.Time
	ConfigSyncState         string `gorm:"size:32;default:''"`
	ConfigPendingSince      *time.Time
	CertSource              string          `gorm:"size:16;default:''"`
	CertID                  int64           `gorm:"default:0;index"`
	Relays                  jsonRelays      `gorm:"column:relays"`
	HideDirect              bool            `gorm:"default:false"`
	ShowRelayStatus         bool            `gorm:"default:false"`
	RelayHealth             jsonRelayHealth `gorm:"column:relay_health"`
	CreatedAt               time.Time
}

func (v400Beta1NodeRow) TableName() string { return "nodes" }

type v400Beta1ClientRow struct {
	ID                       int64  `gorm:"primaryKey;autoIncrement"`
	UserID                   int64  `gorm:"index;not null"`
	PanelID                  int64  `gorm:"not null;index:idx_psp_client_panel_email,priority:1"`
	Email                    string `gorm:"size:255;not null;index:idx_psp_client_panel_email,priority:2"`
	CredClass                int    `gorm:"not null;default:0"`
	UUID                     string `gorm:"size:36;not null;default:''"`
	Password                 string `gorm:"size:128;not null;default:''"`
	DesiredEnable            bool   `gorm:"not null;default:false"`
	DesiredExpiryTime        int64  `gorm:"not null;default:0"`
	PanelQuotaHeadroom       int64  `gorm:"not null;default:0"`
	PanelIPLimit             int    `gorm:"not null;default:0"`
	PanelDeviceLimit         int    `gorm:"not null;default:0"`
	DesiredMinted            bool   `gorm:"not null;default:false"`
	CreatedAt                time.Time
	LifetimeUpBytes          int64  `gorm:"default:0"`
	LifetimeDownBytes        int64  `gorm:"default:0"`
	LifetimeTotalBytes       int64  `gorm:"default:0"`
	LastRawUpBytes           int64  `gorm:"default:0"`
	LastRawDownBytes         int64  `gorm:"default:0"`
	LastRawTotalBytes        int64  `gorm:"default:0"`
	LastCounterEpoch         uint64 `gorm:"default:0"`
	PeriodBaselineUpBytes    int64  `gorm:"default:0"`
	PeriodBaselineDownBytes  int64  `gorm:"default:0"`
	PeriodBaselineTotalBytes int64  `gorm:"default:0"`
}

func (v400Beta1ClientRow) TableName() string { return "psp_clients" }

type v400Beta1AttachmentRow struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	ClientID        int64  `gorm:"not null;index;uniqueIndex:uk_psp_client_inbound,priority:1"`
	NodeID          int64  `gorm:"not null;uniqueIndex:uk_psp_client_inbound,priority:2"`
	FlowOverride    string `gorm:"size:64;not null;default:''"`
	State           string `gorm:"size:16;not null;default:pending"`
	AppliedVersion  uint64 `gorm:"not null;default:0"`
	AppliedEmail    string `gorm:"size:255;not null;default:''"`
	AppliedUUID     string `gorm:"size:36;not null;default:''"`
	AppliedPassword string `gorm:"size:128;not null;default:''"`
	FirstFailedAt   *time.Time
}

func (v400Beta1AttachmentRow) TableName() string { return "psp_client_inbounds" }

type v400Beta1AgentRow struct {
	ID                     int64   `gorm:"primaryKey;autoIncrement"`
	AgentID                string  `gorm:"size:64;not null;uniqueIndex"`
	PanelID                int64   `gorm:"not null;uniqueIndex"`
	Epoch                  uint64  `gorm:"not null;default:1"`
	CredentialSHA256       string  `gorm:"size:64;not null;uniqueIndex"`
	CredentialCiphertext   *string `gorm:"type:text" json:"-"`
	DesiredCoreEngine      string  `gorm:"size:16;not null;default:'xray'"`
	DesiredCoreVersion     string  `gorm:"size:32;not null;default:''"`
	AllowRestrictedReality bool    `gorm:"not null;default:false"`
	ObservedCoreEngine     string  `gorm:"size:16;not null;default:''"`
	LastSeen               *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (v400Beta1AgentRow) TableName() string { return "node_agents" }

type v400Beta1StreamRow struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	AgentID        string `gorm:"size:64;not null;uniqueIndex:uk_node_agent_stream,priority:1"`
	Stream         string `gorm:"size:16;not null;uniqueIndex:uk_node_agent_stream,priority:2"`
	DesiredVersion uint64 `gorm:"not null;default:0"`
	DesiredETag    string `gorm:"column:desired_etag;size:64;not null;default:''"`
	DesiredBody    []byte
	AppliedVersion uint64 `gorm:"not null;default:0"`
	AppliedEpoch   uint64 `gorm:"not null;default:0"`
	AppliedETag    string `gorm:"column:applied_etag;size:64;not null;default:''"`
	PendingSince   *time.Time
	LastSeen       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (v400Beta1StreamRow) TableName() string { return "node_agent_streams" }

type v400Beta1IssueRow struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	AgentID        string `gorm:"size:64;not null;uniqueIndex:uk_node_agent_issue,priority:1;index"`
	Fingerprint    string `gorm:"size:64;not null;uniqueIndex:uk_node_agent_issue,priority:2"`
	Code           string `gorm:"size:128;not null;index"`
	ObjectKey      string `gorm:"column:object_key;size:512;not null"`
	Detail         string `gorm:"type:text;not null"`
	FirstSeenAt    time.Time
	LastSeenAt     time.Time  `gorm:"index"`
	AcknowledgedAt *time.Time `gorm:"index"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (v400Beta1IssueRow) TableName() string { return "node_agent_issues" }

type v400Beta1TaskRow struct {
	TaskID               string                      `gorm:"primaryKey;size:128;index:idx_node_agent_task_offer,priority:4"`
	AgentID              string                      `gorm:"size:64;not null;index:idx_node_agent_task_offer,priority:1;uniqueIndex:uk_node_agent_task_idempotency,priority:1"`
	Kind                 string                      `gorm:"size:96;not null"`
	Args                 []byte                      `gorm:"not null"`
	InputSHA256          string                      `gorm:"size:64;not null"`
	Status               string                      `gorm:"size:16;not null;default:'queued';index:idx_node_agent_task_offer,priority:2;check:chk_node_agent_task_status,status IN ('queued','offered','succeeded','failed','indeterminate')"`
	IdempotencyKeySHA256 *string                     `gorm:"size:64;uniqueIndex:uk_node_agent_task_idempotency,priority:2"`
	SupersedesTaskID     string                      `gorm:"size:128;index"`
	Lifecycle            *nodeAgentTaskLifecycleJSON `gorm:"type:text"`
	DispatchClosedAt     *time.Time
	DispatchClosedReason string `gorm:"size:32;not null;default:''"`
	ResultOK             *bool
	ResultIndeterminate  bool `gorm:"not null;default:false"`
	Result               []byte
	ResultErrorCode      string `gorm:"size:128"`
	ResultError          string `gorm:"type:text"`
	OfferCount           int    `gorm:"not null;default:0"`
	FirstOfferedAt       *time.Time
	LastOfferedAt        *time.Time
	CompletedAt          *time.Time `gorm:"index:idx_node_agent_task_completed"`
	CreatedAt            time.Time  `gorm:"index:idx_node_agent_task_offer,priority:3"`
	UpdatedAt            time.Time
}

func (v400Beta1TaskRow) TableName() string { return "node_agent_tasks" }

type v400Beta1QuarantineRow struct {
	AgentID       string    `gorm:"primaryKey;size:64;not null"`
	TaskID        string    `gorm:"primaryKey;size:128;not null"`
	Payload       []byte    `gorm:"not null"`
	PayloadSHA256 string    `gorm:"size:64;not null"`
	Reason        string    `gorm:"size:32;not null;check:chk_node_agent_task_quarantine_reason,reason IN ('unknown_task','never_offered')"`
	FirstSeenAt   time.Time `gorm:"not null"`
	LastSeenAt    time.Time `gorm:"not null"`
}

func (v400Beta1QuarantineRow) TableName() string { return "node_agent_task_result_quarantines" }

func seedV4Beta1Baseline(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(
		&v392Beta20SchemaMigrationRow{}, &v392Beta20UserRow{}, &v392Beta20GroupRow{}, &v392Beta20PanelRow{},
		&v400Beta1NodeRow{}, &v400Beta1ClientRow{}, &v400Beta1AttachmentRow{}, &v392Beta20SeparatorRow{},
		&v400Beta1AgentRow{}, &v400Beta1StreamRow{}, &v400Beta1IssueRow{}, &v400Beta1TaskRow{}, &v400Beta1QuarantineRow{},
	); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{v392LimitsBaselineMarker, "node_endpoint_desired_observed_v4", "psp_client_inbound_state_v4", "psp_client_inbound_applied_credentials_v1"} {
		if err := db.Create(&v392Beta20SchemaMigrationRow{ID: marker, AppliedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestV400Beta1BaselineUpgradePreservesConvergenceAndNativeEvidence(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	seedV4Beta1Baseline(t, db)
	when := time.Date(2026, 8, 1, 2, 3, 4, 123456000, time.UTC)
	no := false
	node := v400Beta1NodeRow{
		PanelID: 10, InboundID: 21, DisplayName: "beta1 drift", Region: "JP", Enabled: &no,
		DesiredPort: 443, DesiredProtocol: "vless", ObservedPort: 8443, ObservedProtocol: "trojan",
		ConfigPendingSince: &when, LastInboundCounterEpoch: 7, LastInboundSeeded: true, LastInboundTotalBytes: 9007199254740993,
	}
	client := v400Beta1ClientRow{
		UserID: 7, PanelID: 10, Email: "next@example.invalid", UUID: "next-uuid", Password: "next-password", DesiredMinted: true,
		DesiredEnable: true, DesiredExpiryTime: 1893456000000, PanelQuotaHeadroom: 99, PanelIPLimit: 3, LastCounterEpoch: 8, LastRawTotalBytes: 9007199254740993,
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&v400Beta1AttachmentRow{
		ClientID: client.ID, NodeID: node.ID, State: "rejected", AppliedVersion: 4,
		AppliedEmail: "confirmed@example.invalid", AppliedUUID: "confirmed-uuid", AppliedPassword: "confirmed-password", FirstFailedAt: &when,
	}).Error; err != nil {
		t.Fatal(err)
	}
	agentID := "beta1-agent"
	if err := db.Create(&v400Beta1AgentRow{AgentID: agentID, PanelID: 10, Epoch: 9, CredentialSHA256: strings.Repeat("a", 64), DesiredCoreVersion: "26.6.27", LastSeen: &when}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&v400Beta1StreamRow{AgentID: agentID, Stream: "listeners", DesiredVersion: 8, DesiredETag: "desired-etag", DesiredBody: []byte("[]"), AppliedVersion: 4, AppliedEpoch: 9, AppliedETag: "applied-etag", PendingSince: &when}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&v400Beta1IssueRow{AgentID: agentID, Fingerprint: strings.Repeat("b", 64), Code: "native.task.requires_journal", ObjectKey: "native.task/task-1", Detail: "existing evidence", FirstSeenAt: when, LastSeenAt: when, AcknowledgedAt: &when}).Error; err != nil {
		t.Fatal(err)
	}
	args := []byte(`{"existing":true}`)
	// Closed dispatch preserves a real immutable latest-start authorization,
	// rather than inventing expiry for a protected legacy task with nil history.
	deadlineMS := when.UnixMilli() - 1
	lifecycle := &nodeAgentTaskLifecycleJSON{
		IssuedAtMS: deadlineMS - 60_000, NotAfterMS: deadlineMS,
		Policy:                  domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 30, BackupRestoreDays: 30, ResultRetentionDays: 90},
		FullResultRetainUntilMS: deadlineMS + 90*24*60*60*1000,
	}
	firstOffer, lastOffer := when.Add(-30*time.Second), when.Add(-5*time.Second)
	if err := db.Create(&v400Beta1TaskRow{TaskID: "task-1", AgentID: agentID, Kind: "system.inspect", Args: args, InputSHA256: nodeprotocol.ComputeTaskInputSHA256("system.inspect", args), Lifecycle: lifecycle, Status: "offered", OfferCount: 2, FirstOfferedAt: &firstOffer, LastOfferedAt: &lastOffer, DispatchClosedAt: &when, DispatchClosedReason: "expired"}).Error; err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"evidence":"preserve"}`)
	digest := sha256.Sum256(payload)
	if err := db.Create(&v400Beta1QuarantineRow{AgentID: agentID, TaskID: "unknown-task", Payload: payload, PayloadSHA256: hex.EncodeToString(digest[:]), Reason: "unknown_task", FirstSeenAt: when, LastSeenAt: when}).Error; err != nil {
		t.Fatal(err)
	}
	beforeNodes, beforeClients := v392ReadRows[v400Beta1NodeRow](t, db), v392ReadRows[v400Beta1ClientRow](t, db)
	beforeAttachments := v392ReadRows[v400Beta1AttachmentRow](t, db)
	beforeAgents, beforeStreams := v392ReadRows[v400Beta1AgentRow](t, db), v392ReadRows[v400Beta1StreamRow](t, db)
	beforeIssues := v392ReadRows[v400Beta1IssueRow](t, db)
	// Task/quarantine primary keys are not id; read their stable ordering below.
	var beforeTasks []v400Beta1TaskRow
	var beforeQuarantines []v400Beta1QuarantineRow
	if err := db.Order("task_id").Find(&beforeTasks).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Order("agent_id, task_id").Find(&beforeQuarantines).Error; err != nil {
		t.Fatal(err)
	}
	for boot := 1; boot <= 2; boot++ {
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("beta1 baseline V4 boot %d: %v", boot, err)
		}
		if !reflect.DeepEqual(v392ReadRows[v400Beta1NodeRow](t, db), beforeNodes) || !reflect.DeepEqual(v392ReadRows[v400Beta1ClientRow](t, db), beforeClients) ||
			!reflect.DeepEqual(v392ReadRows[v400Beta1AttachmentRow](t, db), beforeAttachments) || !reflect.DeepEqual(v392ReadRows[v400Beta1AgentRow](t, db), beforeAgents) ||
			!reflect.DeepEqual(v392ReadRows[v400Beta1StreamRow](t, db), beforeStreams) || !reflect.DeepEqual(v392ReadRows[v400Beta1IssueRow](t, db), beforeIssues) {
			t.Fatal("beta1 upgrade rewrote independently owned convergence state, native identity, stream or issue evidence")
		}
		var tasks []v400Beta1TaskRow
		var quarantines []v400Beta1QuarantineRow
		if err := db.Order("task_id").Find(&tasks).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Order("agent_id, task_id").Find(&quarantines).Error; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tasks, beforeTasks) || !reflect.DeepEqual(quarantines, beforeQuarantines) {
			t.Fatal("beta1 upgrade fabricated a terminal task result or deleted/rewrote pending/closed/quarantined evidence")
		}
	}
}
