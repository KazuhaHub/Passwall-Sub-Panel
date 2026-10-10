package handler

import (
	"bytes"
	"encoding/json"
	"slices"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// Raw decoding isolates scalar/type errors from config and roster control.
// Inspect size before parsing; only fixed enums may become diagnostic labels.
func (h *NodeSyncHandler) sanitizeAudit(raw json.RawMessage, caps []string, agentID string) *protocol.AuditObservation {
	if len(raw) > protocol.MaxAuditObservationBytes {
		h.recordAuditDrop(agentID, "unknown", "oversized")
		return nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var observation protocol.AuditObservation
	if err := json.Unmarshal(raw, &observation); err != nil {
		h.recordAuditDrop(agentID, "unknown", "invalid")
		return nil
	}
	kind := "unknown"
	switch observation.Kind {
	case "block", "observe", "trial", "usage":
		kind = observation.Kind
	}
	if err := protocol.ValidateAuditObservation(observation); err != nil {
		h.recordAuditDrop(agentID, kind, "invalid")
		return nil
	}
	if !slices.Contains(caps, protocol.CapabilityAuditHits) || (kind == "usage" && !slices.Contains(caps, protocol.CapabilityAuditUsage)) {
		h.recordAuditDrop(agentID, kind, "no_capability")
		return nil
	}
	metrics.NodeAuditReportTotal.With(kind, "accepted").Inc()
	return &observation
}

func (h *NodeSyncHandler) recordAuditDrop(agentID, kind, outcome string) {
	metrics.NodeAuditReportTotal.With(kind, outcome).Inc()
	if h.auditDrops.shouldLog(agentID, time.Now()) {
		// Peer subtree values and validator errors never enter logs, even Debug.
		log.Warn("node audit subtree dropped", "agent_id", agentID, "count", 1)
	}
}
