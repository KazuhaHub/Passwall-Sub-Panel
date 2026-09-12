package domain

import "time"

// NodeAgentTaskStatus is PSP's durable knowledge about a task sent over the
// node-initiated sync channel. Offered deliberately means only that PSP put
// the task in at least one response; it does not claim that the agent received
// or started it.
type NodeAgentTaskStatus string

const (
	NodeAgentTaskQueued    NodeAgentTaskStatus = "queued"
	NodeAgentTaskOffered   NodeAgentTaskStatus = "offered"
	NodeAgentTaskSucceeded NodeAgentTaskStatus = "succeeded"
	NodeAgentTaskFailed    NodeAgentTaskStatus = "failed"
	// Indeterminate means a side effect may have happened before the agent
	// could durably record its outcome. It is terminal and must not be blindly
	// retried as an ordinary failure.
	NodeAgentTaskIndeterminate NodeAgentTaskStatus = "indeterminate"
)

func (s NodeAgentTaskStatus) Valid() bool {
	switch s {
	case NodeAgentTaskQueued, NodeAgentTaskOffered, NodeAgentTaskSucceeded, NodeAgentTaskFailed, NodeAgentTaskIndeterminate:
		return true
	default:
		return false
	}
}

func (s NodeAgentTaskStatus) Terminal() bool {
	return s == NodeAgentTaskSucceeded || s == NodeAgentTaskFailed || s == NodeAgentTaskIndeterminate
}

// NodeAgentTask is one immutable request plus its single immutable terminal
// result. A failed task is never reopened: retry creates a new task ID and may
// point SupersedesTaskID at the failed attempt.
type NodeAgentTask struct {
	TaskID               string
	AgentID              string
	Kind                 string
	Args                 []byte
	InputSHA256          string
	Status               NodeAgentTaskStatus
	IdempotencyKeySHA256 *string
	SupersedesTaskID     string
	// Dispatch closure is orthogonal to result state. A queued task whose
	// result unexpectedly arrived (or a restored task with quarantined evidence)
	// is not safe to offer, but is not completed.
	DispatchClosedAt     *time.Time
	DispatchClosedReason string

	ResultOK            *bool
	ResultIndeterminate bool
	Result              []byte
	ResultErrorCode     string
	ResultError         string

	OfferCount     int
	FirstOfferedAt *time.Time
	LastOfferedAt  *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NodeAgentTaskResult is the storage-neutral form of a result replayed from a
// node outbox. Kind and InputSHA256 bind it to the immutable request, not just
// to an identifier which might have been restored or accidentally reused.
type NodeAgentTaskResult struct {
	TaskID        string
	Kind          string
	InputSHA256   string
	OK            bool
	Indeterminate bool
	Result        []byte
	ErrorCode     string
	Error         string
}

type NodeAgentTaskQuarantineReason string

const (
	NodeAgentTaskQuarantineUnknownTask  NodeAgentTaskQuarantineReason = "unknown_task"
	NodeAgentTaskQuarantineNeverOffered NodeAgentTaskQuarantineReason = "never_offered"
)

// NodeAgentTaskResultQuarantine is immutable agent-local result evidence, not
// a terminal task or a reservation of another agent's task ID. Payload is the
// canonical wire JSON; Result is decoded from that same payload on reads.
type NodeAgentTaskResultQuarantine struct {
	AgentID       string
	TaskID        string
	Payload       []byte
	PayloadSHA256 string
	Reason        NodeAgentTaskQuarantineReason
	Result        NodeAgentTaskResult
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
}
