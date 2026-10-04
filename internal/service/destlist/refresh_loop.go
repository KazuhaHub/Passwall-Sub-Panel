package destlist

import (
	"context"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
)

type RefreshHours func(context.Context) (int, error)

// Start registers exactly one tracked worker with the application's wait group.
// App wiring supplies dest.list_refresh_hours and notifies after saving settings.
func (s *Service) Start(ctx context.Context, wg *sync.WaitGroup, hours RefreshHours, onError func(error)) {
	s.loopOnce.Do(func() { safego.GoTracked(wg, "dest-list-refresh", func() { s.runRefreshLoop(ctx, hours, onError) }) })
}
func (s *Service) NotifySettingsChanged() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) runRefreshLoop(ctx context.Context, hours RefreshHours, onError func(error)) {
	for ctx.Err() == nil {
		value, err := 24, error(nil)
		if hours != nil {
			value, err = hours(ctx)
		}
		if err == nil {
			err = s.RefreshDue(ctx, value)
		}
		if err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
