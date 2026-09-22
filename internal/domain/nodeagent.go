package domain

import (
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

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
	ID                      int64
	AgentID                 string
	PanelID                 int64
	Epoch                   uint64
	CredentialSHA256        string
	ObservedProtocolVersion int
	ObservedCapabilities    []string
	ProtocolObservedAt      *time.Time
	DesiredCoreEngine       NodeCoreEngine
	DesiredCoreVersion      string
	AllowRestrictedReality  bool
	ObservedCoreEngine      NodeCoreEngine
	LastSeen                *time.Time
	// Refused* record the most recent authenticated report this panel REFUSED at
	// the wire boundary, which is a different fact from an observation and is
	// stored in different columns for that reason.
	//
	// WHY IT CANNOT SHARE THE OBSERVATION COLUMNS. An observation is what the
	// panel accepted and still acts on; a refusal is what it would not accept.
	// Writing a refused generation into ObservedProtocolVersion would destroy the
	// last thing the panel actually knows about the node — and that record is
	// what compat-policy 3.2 means by keeping the last valid observation when
	// contact is lost. canonicalProtocolCapabilities refuses the value anyway, so
	// the attempt would fail rather than corrupt; these columns exist so the fact
	// has somewhere true to go.
	//
	// RefusedFirstAt is the first refusal of the current run and does not move
	// while refusals continue, so "since when" survives a node that retries every
	// thirty seconds. Both are cleared the moment a report is accepted again: a
	// refusal is a current fact, never a sticky mark, for the same reason
	// capabilities are (ADR 0033 section 2).
	RefusedProtocolVersion *int
	RefusedReason          string
	RefusedFirstAt         *time.Time
	RefusedAt              *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// Refusal reasons. They are stable strings: an operator-facing message and a
// metric label are both derived from them.
const (
	// NodeRefusalProtocolGeneration is a report whose wire generation is outside
	// the range this panel declares.
	NodeRefusalProtocolGeneration = "protocol_generation"
	// NodeRefusalReportInvalid is a report this panel could not accept for any
	// other structural reason. It is deliberately one bucket: the detail belongs
	// in the log, not in a column an unauthenticated-shaped value could grow.
	NodeRefusalReportInvalid = "report_invalid"
)

// CurrentlyRefused reports whether the last thing this panel did with a report
// from this agent was refuse it.
//
// A refusal older than the last accepted observation means the node recovered,
// so the comparison is against the observation rather than against the presence
// of the columns.
func (a *NodeAgent) CurrentlyRefused() bool {
	if a == nil || a.RefusedAt == nil {
		return false
	}
	if a.ProtocolObservedAt == nil {
		return true
	}
	return !a.RefusedAt.Before(*a.ProtocolObservedAt)
}

// SupportedNodeProtocolGenerations is PSP's OWN declaration of which wire
// generations this binary speaks.
//
// It lives here rather than in the shared protocol module because it is a
// property of this binary, not of the contract. When the range was compiled into
// the shared package, adding a generation there widened what PSP accepts without
// anyone reviewing that decision here — a dependency upgrade granting a
// capability. Widening this is now a change to this file, in this repository.
func SupportedNodeProtocolGenerations() nodeprotocol.GenerationRange {
	return nodeprotocol.GenerationRange{
		Min: nodeprotocol.ProtocolVersion1,
		Max: nodeprotocol.ProtocolVersion1,
	}
}

// ProtocolCompatibility returns the shared PSP/Node compatibility decision
// for the most recent authenticated report. The bool is false until at least
// one report has been persisted; a zero protocol version after that point is
// the explicitly supported legacy spelling of protocol v1.
func (a *NodeAgent) ProtocolCompatibility() (nodeprotocol.Compatibility, bool) {
	if a == nil || a.ProtocolObservedAt == nil {
		return nodeprotocol.Compatibility{}, false
	}
	return nodeprotocol.AssessCompatibilityIn(a.ObservedProtocolVersion, a.ObservedCapabilities, SupportedNodeProtocolGenerations()), true
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
	return s != nil && nodeprotocol.Converged(nodeprotocol.ETag(s.AppliedETag), nodeprotocol.ETag(s.DesiredETag))
}
