package ports

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The staff group list reads only mode/stage for its requested page. No owned
// list IDs, list contents or administrator-only definitions cross this port.
type DestGroupModeReadRepo interface {
	ReadDestinationGroupModes(context.Context, []int64) (map[int64]domain.DestGroupAccessMode, error)
}

// DestinationGroupModeRepo commits a mode and its two private custom lists
// under the definition-generation lock. Initial list contents must be parsed
// before calling the repository; existing owned contents are preserved.
type DestinationGroupModeRepo interface {
	GetGroupMode(context.Context, int64) (domain.DestGroupMode, error)
	SaveGroupMode(context.Context, *domain.DestGroupMode, time.Time, [2]domain.DestList, time.Time) error
}
