package destpolicy

import (
	"context"
	"fmt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Called only after the candidate decision transaction commits. A failed
// issue write is not cached, so a cached candidate can retry it next sync.
func (c *Compiler) recordCompilationIssue(ctx context.Context, agentID string, p *PreparedCandidate) error {
	issue := domain.NodeAgentIssue{AgentID: agentID, Key: p.context}
	switch {
	case p.limit != nil:
		issue.Code = "destination_policy_over_limit"
		issue.Detail = "The access policy exceeds this node's subject or document size limit."
	case len(p.precheck) > 0:
		issue.Code = "destination_policy_sniffing_insufficient"
		issue.Detail = "The inbound sniffing settings cannot execute the access policy."
	default:
		// A subsequent fault in the same context is a new occurrence. Persisted
		// issue deduplication still preserves the operator's acknowledgement.
		c.issuesWritten.Delete(agentID)
		return nil
	}
	if c.observer.issues == nil {
		return nil
	}
	signature := issue.Code + "\x00" + issue.Key
	if prior, found := c.issuesWritten.Get(agentID); found && prior == signature {
		return nil
	}
	if err := c.observer.issues.RecordBatch(ctx, agentID, []domain.NodeAgentIssue{issue}, c.now().UTC()); err != nil {
		return fmt.Errorf("record destination compilation issue: %w", err)
	}
	c.issuesWritten.Put(agentID, signature, len(agentID)+len(signature)+64)
	return nil
}
