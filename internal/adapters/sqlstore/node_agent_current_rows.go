package sqlstore

import (
	"database/sql"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Read every public model column on each call. CredentialCiphertext is private
// and was already omitted by the list and panel lookup methods. JSON failures still
// fail the entire read, including when earlier rows were decoded successfully.
const currentNodeAgentColumns = `id,agent_id,panel_id,epoch,credential_sha256,
observed_protocol_version,observed_capabilities,protocol_observed_at,
desired_core_engine,desired_core_version,allow_restricted_reality,observed_core_engine,last_seen,
refused_protocol_version,refused_reason,refused_first_at,refused_at,created_at,updated_at`

func loadCurrentNodeAgentRows(query *gorm.DB) ([]*domain.NodeAgent, error) {
	rows, err := query.Model(&nodeAgentRow{}).Select(currentNodeAgentColumns).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.NodeAgent, 0)
	var row nodeAgentRow
	var created, updated sql.NullTime
	// Use the existing current-row scalar adapter for pgx's Go 1.27 column
	// scanner too. It preserves database/sql conversions without classifying
	// every scalar pointer again for each cell; other drivers keep their path.
	destinations := currentSQLDestinations(query.Dialector.Name(), []any{
		&row.ID, &row.AgentID, &row.PanelID, &row.Epoch, &row.CredentialSHA256,
		&row.ObservedProtocolVersion, &row.ObservedCapabilities, &row.ProtocolObservedAt,
		&row.DesiredCoreEngine, &row.DesiredCoreVersion, &row.AllowRestrictedReality, &row.ObservedCoreEngine, &row.LastSeen,
		&row.RefusedProtocolVersion, &row.RefusedReason, &row.RefusedFirstAt, &row.RefusedAt, &created, &updated,
	})
	for rows.Next() {
		// Reset values, not the stable destination slots. Nullable pointers and
		// JSON must allocate independently rather than mutate a previous result.
		row, created, updated = nodeAgentRow{}, sql.NullTime{}, sql.NullTime{}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		row.CreatedAt, row.UpdatedAt = created.Time, updated.Time
		out = append(out, rowToNodeAgent(&row))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
