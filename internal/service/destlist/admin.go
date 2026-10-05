package destlist

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func (s *Service) Categories(ctx context.Context) ([]Category, time.Time, error) {
	if s == nil || s.cache == nil {
		return nil, time.Time{}, domain.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	catalog, at, err := s.cache.Cached()
	if err != nil {
		return nil, time.Time{}, err
	}
	return catalog.Categories(), at, nil
}

func (s *Service) RefreshCategories(ctx context.Context) error {
	if s == nil || s.cache == nil {
		return domain.ErrUnavailable
	}
	return s.operationGate.RunRead(ctx, func(ctx context.Context) error { return s.cache.Refresh(ctx) })
}

func (s *Service) IsRefreshing(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshing[id] > 0
}

// QueueRefresh validates the source before responding. The lifecycle dispatcher
// owns the work after HTTP cancellation; RefreshList owns operation admission.
func (s *Service) QueueRefresh(ctx context.Context, id int64, dispatch func(string, func(context.Context))) error {
	if dispatch == nil {
		return domain.ErrUnavailable
	}
	list, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if list.Kind != domain.DestListRemote && list.Kind != domain.DestListGeosite {
		return domain.ErrValidation
	}
	s.mu.Lock()
	s.refreshing[id]++
	s.mu.Unlock()
	dispatch("destination.list-refresh", func(ctx context.Context) {
		defer func() {
			s.mu.Lock()
			s.refreshing[id]--
			if s.refreshing[id] == 0 {
				delete(s.refreshing, id)
			}
			s.mu.Unlock()
		}()
		_ = s.RefreshList(ctx, id)
	})
	return nil
}
