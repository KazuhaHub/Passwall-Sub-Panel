package ports

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ServerMigrationRepo is an offline maintenance operation. The HTTP surface
// exposes Load only; Apply must never run beside a live PSP process.
type ServerMigrationRepo interface {
	Load(ctx context.Context, panelID int64) (*domain.ServerMigrationSnapshot, error)
	Apply(ctx context.Context, panelID int64, expectedFingerprint string, agent *domain.NodeAgent, credential string) error
}
