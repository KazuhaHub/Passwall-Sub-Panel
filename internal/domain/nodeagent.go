package domain

import "time"

type NodeCoreEngine string

const (
	NodeCoreXray    NodeCoreEngine = "xray"
	NodeCoreSingBox NodeCoreEngine = "sing-box"
)

func NormalizeNodeCoreEngine(engine NodeCoreEngine) NodeCoreEngine {
	if engine == "" {
		return NodeCoreXray
	}
	return engine
}

func (e NodeCoreEngine) Valid() bool {
	switch e {
	case NodeCoreXray, NodeCoreSingBox:
		return true
	default:
		return false
	}
}

// NodeAgent is PSP's durable identity for one native node process. AgentID is
// minted at registration and never derived from an address; PanelID enforces
// the protocol's one-agent-to-one-panel accounting scope. Authentication and
// ordinary domain reads expose only a SHA-256 credential digest. A separate
// encrypted recovery copy is accessed through the admin provisioning port.
type NodeAgent struct {
	ID                     int64
	AgentID                string
	PanelID                int64
	Epoch                  uint64
	CredentialSHA256       string
	DesiredCoreEngine      NodeCoreEngine
	DesiredCoreVersion     string
	AllowRestrictedReality bool
	ObservedCoreEngine     NodeCoreEngine
	LastSeen               *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// NodeAgentIssue is an operator-visible condition reported by a native agent.
// Equal (agent, code, key, detail) reports collapse into one row whose
// LastSeenAt advances. Acknowledgement is review state, not resolution: the v1
// protocol deliberately has no agent-authored "resolved" assertion.
type NodeAgentIssue struct {
	ID             int64      `json:"id"`
	AgentID        string     `json:"agent_id"`
	Code           string     `json:"code"`
	Key            string     `json:"key,omitempty"`
	Detail         string     `json:"detail,omitempty"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// OfflineAt deliberately reads only LastSeen. Desired/applied versions and
// ETags say whether content converged; they are never liveness signals.
func (a *NodeAgent) OfflineAt(now time.Time, maxSilence time.Duration) bool {
	if a == nil || a.LastSeen == nil || maxSilence <= 0 {
		return true
	}
	return now.Sub(*a.LastSeen) > maxSilence
}

type NodeAgentStreamName string

const (
	NodeAgentStreamConfig     NodeAgentStreamName = "config"
	NodeAgentStreamRoster     NodeAgentStreamName = "roster"
	NodeAgentStreamDirectives NodeAgentStreamName = "directives"
)

func (s NodeAgentStreamName) Valid() bool {
	switch s {
	case NodeAgentStreamConfig, NodeAgentStreamRoster, NodeAgentStreamDirectives:
		return true
	default:
		return false
	}
}

// NodeAgentStream is the desired/applied state of one independently-versioned
// stream. DesiredBody contains canonical JSON for the segment body. Epoch lives
// on NodeAgent because all three streams reset together when the row is rebuilt.
type NodeAgentStream struct {
	AgentID string
	Stream  NodeAgentStreamName

	DesiredVersion uint64
	DesiredETag    string
	DesiredBody    []byte

	AppliedVersion uint64
	// AppliedEpoch is reported by the agent alongside AppliedVersion. Keeping
	// it durable prevents a pre-restore acknowledgement from being mistaken for
	// convergence after PSP rebuilds an agent epoch and versions restart.
	AppliedEpoch uint64
	AppliedETag  string
	PendingSince *time.Time
	LastSeen     *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Converged compares content identity, never version. This correctly treats an
// A/B/A rollback as converged when the agent still holds byte-identical A.
func (s *NodeAgentStream) Converged() bool {
	return s != nil && s.DesiredETag != "" && s.DesiredETag == s.AppliedETag
}
