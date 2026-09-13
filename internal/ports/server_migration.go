package ports

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ServerMigrationRepo atomically changes the saved backend. Apply requires
// either stopped PSP processes or the single-instance operation gate drained
// across upstream I/O and its subsequent writes; a row lock alone is insufficient.
type ServerMigrationRepo interface {
	Load(ctx context.Context, panelID int64) (*domain.ServerMigrationSnapshot, error)
	Apply(ctx context.Context, panelID int64, expectedFingerprint string, agent *domain.NodeAgent, credential string) error
}
