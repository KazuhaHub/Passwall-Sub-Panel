package destpolicy

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
)

type ExemptionStore interface {
	ListExemptions(context.Context) ([]domain.DestExemption, error)
	GetExemption(context.Context, int64) (domain.DestExemption, error)
	ExemptionUserUPNs(context.Context, []int64) (map[int64]string, error)
	SaveExemption(context.Context, *domain.DestExemption, bool, time.Time) error
	DeleteExemption(context.Context, int64, time.Time) error
}
type ExemptionView struct {
	Exemption         domain.DestExemption
	UPN, CreatedByUPN *string
	Expired           bool
}
type ExemptionManager struct {
	store ExemptionStore
	gate  *operationgate.Gate
	now   func() time.Time
}

func NewExemptionManager(store ExemptionStore) *ExemptionManager {
	return &ExemptionManager{store: store, now: time.Now}
}
func (m *ExemptionManager) SetOperationGate(gate *operationgate.Gate) { m.gate = gate }
func (m *ExemptionManager) available() error {
	if m == nil || m.store == nil {
		return domain.ErrUnavailable
	}
	return nil
}
func exemptionUPN(names map[int64]string, id int64) *string {
	value, exists := names[id]
	if !exists {
		return nil
	}
	return &value
}
func exemptionView(ex domain.DestExemption, names map[int64]string, now time.Time) ExemptionView {
	return ExemptionView{Exemption: ex, UPN: exemptionUPN(names, ex.UserID), CreatedByUPN: exemptionUPN(names, ex.CreatedBy), Expired: ex.ExpiresAt != nil && !ex.ExpiresAt.After(now)}
}
func (m *ExemptionManager) List(ctx context.Context) ([]ExemptionView, error) {
	if err := m.available(); err != nil {
		return nil, err
	}
	ctx, release, err := m.gate.Read(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := m.store.ListExemptions(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, 2*len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID, row.CreatedBy)
	}
	names, err := m.store.ExemptionUserUPNs(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := m.now().UTC()
	result := make([]ExemptionView, 0, len(rows))
	for _, row := range rows {
		result = append(result, exemptionView(row, names, now))
	}
	slices.SortFunc(result, func(a, b ExemptionView) int {
		if a.Expired != b.Expired {
			if a.Expired {
				return 1
			}
			return -1
		}
		an, bn := "", ""
		if a.UPN != nil {
			an = *a.UPN
		}
		if b.UPN != nil {
			bn = *b.UPN
		}
		if c := strings.Compare(an, bn); c != 0 {
			return c
		}
		if a.Exemption.UserID < b.Exemption.UserID {
			return -1
		}
		if a.Exemption.UserID > b.Exemption.UserID {
			return 1
		}
		return 0
	})
	return result, nil
}
func (m *ExemptionManager) Get(ctx context.Context, userID int64) (ExemptionView, error) {
	if err := m.available(); err != nil {
		return ExemptionView{}, err
	}
	if userID <= 0 {
		return ExemptionView{}, invalid("user_id")
	}
	ctx, release, err := m.gate.Read(ctx)
	if err != nil {
		return ExemptionView{}, err
	}
	defer release()
	ex, err := m.store.GetExemption(ctx, userID)
	if err != nil {
		return ExemptionView{}, err
	}
	names, err := m.store.ExemptionUserUPNs(ctx, []int64{ex.UserID, ex.CreatedBy})
	if err != nil {
		return ExemptionView{}, err
	}
	return exemptionView(ex, names, m.now().UTC()), nil
}
func (m *ExemptionManager) Save(ctx context.Context, ex domain.DestExemption, actorID int64, create bool) (ExemptionView, error) {
	if err := m.available(); err != nil {
		return ExemptionView{}, err
	}
	if ex.UserID <= 0 {
		return ExemptionView{}, invalid("user_id")
	}
	if strings.TrimSpace(ex.Reason) == "" || utf8.RuneCountInString(ex.Reason) > 255 {
		return ExemptionView{}, invalid("reason")
	}
	if ex.ExpiresAt != nil && ex.ExpiresAt.UnixMilli() <= 0 {
		return ExemptionView{}, invalid("expires_at")
	}
	ctx, release, err := m.gate.Read(ctx)
	if err != nil {
		return ExemptionView{}, err
	}
	defer release()
	if create {
		if actorID <= 0 {
			return ExemptionView{}, domain.ErrUnavailable
		}
		ex.CreatedBy = actorID
	} else {
		old, err := m.store.GetExemption(ctx, ex.UserID)
		if err != nil {
			return ExemptionView{}, err
		}
		ex.CreatedBy, ex.CreatedAt = old.CreatedBy, old.CreatedAt
	}
	names, err := m.store.ExemptionUserUPNs(ctx, []int64{ex.UserID, ex.CreatedBy})
	if err != nil {
		return ExemptionView{}, err
	}
	if _, exists := names[ex.UserID]; !exists {
		return ExemptionView{}, domain.ErrNotFound
	}
	if create {
		if _, exists := names[ex.CreatedBy]; !exists {
			return ExemptionView{}, domain.ErrUnavailable
		}
	}
	now := m.now().UTC()
	if err := m.store.SaveExemption(ctx, &ex, create, now); err != nil {
		return ExemptionView{}, err
	}
	return exemptionView(ex, names, m.now().UTC()), nil
}
func (m *ExemptionManager) Delete(ctx context.Context, userID int64) error {
	if err := m.available(); err != nil {
		return err
	}
	if userID <= 0 {
		return invalid("user_id")
	}
	return m.gate.RunRead(ctx, func(ctx context.Context) error { return m.store.DeleteExemption(ctx, userID, m.now().UTC()) })
}
