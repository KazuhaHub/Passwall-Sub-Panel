package destlist

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"golang.org/x/sync/singleflight"
)

type DefinitionStore interface {
	GetList(context.Context, int64) (domain.DestList, error)
	ListRefreshTargets(context.Context) ([]domain.DestList, error)
	SaveList(context.Context, *domain.DestList, time.Time, time.Time) error
	CommitListRefresh(context.Context, domain.DestList, domain.DestListRefresh, time.Time) error
}

type listAttempt struct {
	source string
	at     time.Time
}
type Service struct {
	store           DefinitionStore
	fetcher         *Fetcher
	cache           *GeositeCache
	now             func() time.Time
	flights, rounds singleflight.Group
	mu              sync.Mutex
	attempts        map[int64]listAttempt
	wake            chan struct{}
	loopOnce        sync.Once
	operationGate   *operationgate.Gate
	validateSave    func(context.Context, domain.DestList) error
	refreshing      map[int64]int
}

func NewService(store DefinitionStore, cache *GeositeCache) *Service {
	return &Service{store: store, cache: cache, fetcher: NewFetcher(), now: time.Now, attempts: map[int64]listAttempt{}, refreshing: map[int64]int{}, wake: make(chan struct{}, 1)}
}

// Configure at assembly time. Admission spans downloads and their final writes,
// so an online backend switch cannot cross an old backend's pending response.
func (s *Service) SetOperationGate(gate *operationgate.Gate) { s.operationGate = gate }

// Nil leaves parser/source validation enabled but omits fleet definition quota
// checks. Production assembly supplies the same checker used by publication.
func (s *Service) SetSaveValidator(validate func(context.Context, domain.DestList) error) {
	s.validateSave = validate
}

// Preview is read-only, including a missing geosite cache. Downloading the
// shared catalog is an explicit action, never a side effect of opening a form.
func (s *Service) Preview(ctx context.Context, list domain.DestList) (FetchResult, error) {
	ctx, release, err := s.operationGate.Read(ctx)
	if err != nil {
		return FetchResult{}, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return FetchResult{}, err
	}
	switch list.Kind {
	case domain.DestListCustom:
		p, err := ParseCustom(list.SourceText)
		return FetchResult{Parsed: p, Bytes: len(list.SourceText)}, err
	case domain.DestListRemote:
		return s.fetcher.Fetch(ctx, list.SourceURL)
	case domain.DestListGeosite:
		if s.cache == nil {
			return FetchResult{}, domain.ErrUnavailable
		}
		c, _, err := s.cache.Cached()
		if err != nil {
			return FetchResult{}, err
		}
		p, err := c.Select(list.GeositeCategory, attributeFields(list.GeositeAttrs))
		return FetchResult{Parsed: p}, err
	default:
		return FetchResult{}, domain.ErrValidation
	}
}
func attributeFields(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

func (s *Service) Save(ctx context.Context, list *domain.DestList, expected time.Time) error {
	ctx, release, err := s.operationGate.Read(ctx)
	if err != nil {
		return err
	}
	defer release()
	if s.store == nil {
		return domain.ErrUnavailable
	}
	if list == nil || list.ID < 0 || strings.TrimSpace(list.Name) == "" || utf8.RuneCountInString(list.Name) > 128 {
		return domain.ErrValidation
	}
	candidate := *list
	candidate.Name = strings.TrimSpace(candidate.Name)
	var old domain.DestList
	if candidate.ID != 0 {
		var err error
		old, err = s.store.GetList(ctx, candidate.ID)
		if err != nil {
			return err
		}
		if old.Kind != candidate.Kind {
			return domain.ErrValidation
		}
		if !old.UpdatedAt.Equal(expected) {
			return fmt.Errorf("%w: dest_list_stale", domain.ErrConflict)
		}
	}
	result, err := s.Preview(ctx, candidate)
	now := s.now().UTC()
	if err != nil {
		// URL validation, broad entries and parser failures cannot be saved as a
		// successful definition. Transient remote failures may save a pending source.
		if candidate.Kind != domain.DestListRemote || errors.Is(err, domain.ErrValidation) || ctx.Err() != nil {
			return err
		}
		candidate.Entries, candidate.EntryCount, candidate.RegexpCount = old.Entries, old.EntryCount, old.RegexpCount
		candidate.ContentSHA256, candidate.ParseReport, candidate.LastFetchedAt = old.ContentSHA256, old.ParseReport, old.LastFetchedAt
		candidate.LastError = boundedText(err.Error())
	} else {
		candidate.Entries, candidate.EntryCount, candidate.RegexpCount = result.Parsed.Entries, result.Parsed.EntryCount, result.Parsed.RegexpCount
		candidate.ContentSHA256, candidate.ParseReport, candidate.LastError = result.Parsed.ContentSHA256, &result.Parsed.Report, ""
		if candidate.Kind != domain.DestListCustom {
			candidate.LastFetchedAt = &now
		} else {
			candidate.LastFetchedAt = nil
		}
	}
	if candidate.Kind != domain.DestListCustom {
		candidate.SourceText = nil
	}
	if s.validateSave != nil {
		if err := s.validateSave(ctx, candidate); err != nil {
			return err
		}
	}
	if err := s.store.SaveList(ctx, &candidate, expected, now); err != nil {
		return err
	}
	*list = candidate
	return nil
}

func (s *Service) Get(ctx context.Context, id int64) (domain.DestList, error) {
	if s == nil || s.store == nil {
		return domain.DestList{}, domain.ErrUnavailable
	}
	ctx, release, err := s.operationGate.Read(ctx)
	if err != nil {
		return domain.DestList{}, err
	}
	defer release()
	return s.store.GetList(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if s == nil || s.store == nil {
		return domain.ErrUnavailable
	}
	store, ok := s.store.(interface {
		DeleteList(context.Context, int64, time.Time) error
	})
	if !ok {
		return domain.ErrUnavailable
	}
	return s.operationGate.RunRead(ctx, func(ctx context.Context) error { return store.DeleteList(ctx, id, s.now()) })
}

func (s *Service) RefreshList(ctx context.Context, id int64) error {
	if s.store == nil {
		return domain.ErrUnavailable
	}
	if id <= 0 {
		return domain.ErrValidation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.operationGate.RunRead(ctx, func(ctx context.Context) error {
		return s.withListFlight(ctx, id, func() (any, error) {
			captured, err := s.store.GetList(ctx, id)
			if err != nil {
				return nil, err
			}
			if captured.Kind != domain.DestListRemote && captured.Kind != domain.DestListGeosite {
				return nil, domain.ErrValidation
			}
			var cacheError error
			if captured.Kind == domain.DestListGeosite {
				if s.cache == nil {
					cacheError = domain.ErrUnavailable
				} else {
					cacheError = s.cache.Refresh(ctx)
				}
			}
			return nil, s.refreshCaptured(ctx, captured, cacheError)
		})
	})
}

func (s *Service) withListFlight(ctx context.Context, id int64, work func() (any, error)) error {
	result := s.flights.DoChan(strconv.FormatInt(id, 10), func() (any, error) {
		s.mu.Lock()
		s.refreshing[id]++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.refreshing[id]--
			if s.refreshing[id] == 0 {
				delete(s.refreshing, id)
			}
			s.mu.Unlock()
		}()
		return work()
	})
	select {
	case <-ctx.Done():
		// Join the flight before releasing lifecycle/admission ownership.
		// Its context has been canceled, but database/download cleanup may
		// still be running. A shared flight's leader must drain as well.
		<-result
		return ctx.Err()
	case result := <-result:
		return result.Err
	}
}

func (s *Service) refreshCaptured(ctx context.Context, captured domain.DestList, cacheError error) error {
	outcome := metrics.DestListRefreshFailed
	// Record once in the actual single-flight work, not for every waiter.
	defer func() { metrics.DestListRefreshTotal.With(outcome).Inc() }()
	result, err := FetchResult{}, cacheError
	if err == nil {
		result, err = s.Preview(ctx, captured)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	refresh := domain.DestListRefresh{}
	if err != nil {
		refresh.LastError = boundedText(err.Error())
	} else {
		refresh.Entries, refresh.EntryCount, refresh.RegexpCount = result.Parsed.Entries, result.Parsed.EntryCount, result.Parsed.RegexpCount
		refresh.ContentSHA256, refresh.ParseReport = result.Parsed.ContentSHA256, &result.Parsed.Report
	}
	if commitErr := s.store.CommitListRefresh(ctx, captured, refresh, s.now().UTC()); commitErr != nil {
		return commitErr
	}
	var parseError *Error
	if errors.As(err, &parseError) && parseError.Code == "broad_entry" {
		outcome = metrics.DestListRefreshBroad
	} else if err == nil {
		switch {
		case result.Parsed.Report.IgnoredBroad > 0:
			outcome = metrics.DestListRefreshBroad
		case result.Parsed.ContentSHA256 == captured.ContentSHA256:
			outcome = metrics.DestListRefreshUnchanged
		default:
			outcome = metrics.DestListRefreshUpdated
		}
	}
	return err
}

// RefreshDue downloads the shared category catalog once per round. Selection
// and HTTP parsing occur outside definition transactions; commit rechecks the
// captured row/source. Attempts bound repeated failures to the current interval.
func (s *Service) RefreshDue(ctx context.Context, hours int) error {
	return s.operationGate.RunRead(ctx, func(ctx context.Context) error { return s.refreshDue(ctx, hours) })
}

func (s *Service) refreshDue(ctx context.Context, hours int) error {
	if s.store == nil {
		return domain.ErrUnavailable
	}
	if hours < 6 || hours > 168 {
		return domain.ErrValidation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result := s.rounds.DoChan("round", func() (any, error) {
		targets, err := s.store.ListRefreshTargets(ctx)
		if err != nil {
			return nil, err
		}
		now, interval := s.now().UTC(), time.Duration(hours)*time.Hour
		due := make([]domain.DestList, 0, len(targets))
		geosite := false
		alive := map[int64]bool{}
		s.mu.Lock()
		for _, target := range targets {
			alive[target.ID] = true
			source := string(target.Kind) + "\x00" + target.SourceURL + "\x00" + target.GeositeCategory + "\x00" + target.GeositeAttrs
			previous := s.attempts[target.ID]
			if target.LastFetchedAt != nil && now.Sub(*target.LastFetchedAt) < interval {
				continue
			}
			if previous.source == source && now.Sub(previous.at) < interval {
				continue
			}
			s.attempts[target.ID] = listAttempt{source: source, at: now}
			due = append(due, target)
			geosite = geosite || target.Kind == domain.DestListGeosite
		}
		for id := range s.attempts {
			if !alive[id] {
				delete(s.attempts, id)
			}
		}
		s.mu.Unlock()
		var cacheError error
		if geosite {
			if s.cache == nil {
				cacheError = domain.ErrUnavailable
			} else {
				cacheError = s.cache.Refresh(ctx)
			}
		}
		var failures []error
		for _, captured := range due {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			priorError := error(nil)
			if captured.Kind == domain.DestListGeosite {
				priorError = cacheError
			}
			if err := s.withListFlight(ctx, captured.ID, func() (any, error) { return nil, s.refreshCaptured(ctx, captured, priorError) }); err != nil {
				failures = append(failures, err)
			}
		}
		return nil, errors.Join(failures...)
	})
	select {
	case <-ctx.Done():
		<-result
		return ctx.Err()
	case result := <-result:
		return result.Err
	}
}
