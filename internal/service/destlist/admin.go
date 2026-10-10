package destlist

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func (s *Service) Categories(ctx context.Context) ([]Category, time.Time, error) {
	categories, at, _, err := s.CategoryView(ctx)
	return categories, at, err
}

type CategoryRefreshStatus struct {
	Refreshing bool   `json:"refreshing"`
	LastError  string `json:"last_error"`
}

// Snapshot status and cached content together so a completion cannot pair an
// unavailable catalog with a settled status and stop its caller's polling.
// Cached reads take no network or filesystem locks.
func (s *Service) CategoryView(ctx context.Context) ([]Category, time.Time, CategoryRefreshStatus, error) {
	if s == nil || s.cache == nil {
		return nil, time.Time{}, CategoryRefreshStatus{}, domain.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, CategoryRefreshStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := CategoryRefreshStatus{Refreshing: s.catalogQueued || s.catalogActive > 0}
	if !state.Refreshing {
		state.LastError = s.catalogError
		if state.LastError == "" {
			state.LastError = s.cache.LastError()
		}
	}
	catalog, at, err := s.cache.Cached()
	if err != nil {
		return nil, time.Time{}, state, err
	}
	return catalog.Categories(), at, state, nil
}

func (s *Service) RefreshCategories(ctx context.Context) error {
	if s == nil || s.cache == nil {
		return domain.ErrUnavailable
	}
	s.mu.Lock()
	s.catalogActive++
	s.catalogError = ""
	s.mu.Unlock()
	err := s.operationGate.RunRead(ctx, func(ctx context.Context) error { return s.cache.Refresh(ctx) })
	s.mu.Lock()
	s.catalogActive--
	s.catalogError = ""
	if err != nil {
		s.catalogError = err.Error()
	}
	s.mu.Unlock()
	return err
}

// Reserve pending status before dispatch, including time spent waiting for the
// lifecycle worker or operation admission. Duplicate requests share the job.
func (s *Service) QueueCategoryRefresh(ctx context.Context, dispatch func(string, func(context.Context))) error {
	if s == nil || s.cache == nil || dispatch == nil {
		return domain.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.catalogQueued || s.catalogActive > 0 {
		s.mu.Unlock()
		return nil
	}
	s.catalogQueued, s.catalogError = true, ""
	s.mu.Unlock()
	dispatch("destination.geosite-refresh", func(ctx context.Context) {
		defer func() { s.mu.Lock(); s.catalogQueued = false; s.mu.Unlock() }()
		_ = s.RefreshCategories(ctx)
	})
	return nil
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
