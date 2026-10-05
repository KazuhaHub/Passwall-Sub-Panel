package ports

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// DestinationGroupModeRepo commits a mode and its two private custom lists
// under the definition-generation lock. Initial list contents must be parsed
// before calling the repository; existing owned contents are preserved.
type DestinationGroupModeRepo interface {
	GetGroupMode(context.Context, int64) (domain.DestGroupMode, error)
	SaveGroupMode(context.Context, *domain.DestGroupMode, time.Time, [2]domain.DestList, time.Time) error
}
