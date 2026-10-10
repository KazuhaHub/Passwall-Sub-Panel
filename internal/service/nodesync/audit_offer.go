package nodesync

import (
	"slices"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// AuditCollector accepts best-effort telemetry after a valid control response
// has been built. No return value can change that control response.
type AuditCollector interface {
	Offer(agentID string, panelID int64, receivedAt time.Time, body protocol.AuditObservation)
}

func auditOfferEligible(capabilities []string, audit *protocol.AuditObservation) bool {
	return audit != nil && slices.Contains(capabilities, protocol.CapabilityAuditHits) &&
		(audit.Kind != "usage" || slices.Contains(capabilities, protocol.CapabilityAuditUsage)) &&
		protocol.ValidateAuditObservation(*audit) == nil
}
