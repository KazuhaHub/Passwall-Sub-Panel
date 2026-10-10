package ports

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DestRiskReadRepo exposes only the bulk, address-free fixed-window read.
// It cannot ingest hits, change policy or modify account service state.
type DestRiskReadRepo interface {
	ReadDestinationRiskWindow(ctx context.Context, since, until time.Time) (domain.DestRiskWindow, error)
}
