package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// settings builds a fully-configured emergency-access UISettings stub.
// Individual tests override fields they care about.
func emSettings() ports.UISettings {
	return ports.UISettings{
		EmergencyAccessEnabled:  true,
		EmergencyAccessHours:    24,
		EmergencyAccessMaxCount: 1,
		EmergencyAccessQuotaGB:  10,
	}
}

// Regression: a single-use user inside their active window must report
// "active", not "no_quota" — used_count has already hit max_count by virtue of
// opening the window. The previous order checked remaining FIRST and showed
// "次数已用完" while the window was still ticking, which made the user think
// they were locked out when they weren't.
func TestEmergencyStatus_ActiveWindowWinsOverRemainingZero(t *testing.T) {
	now := time.Now()
	until := now.Add(2 * time.Hour)
	u := &domain.User{
		ID:                     1,
		Enabled:                true,
		EmergencyUsedCount:     1, // used it once
		EmergencyUntil:         &until,
		LifetimeTotalBytes:     5_000_000_000,
		EmergencyBaselineBytes: 1_000_000_000,
	}
	st := EmergencyAccessStatusForUserWithTrafficLimit(u, emSettings(), now, false)
	if st.Status != "active" {
		t.Fatalf("status = %q, want active (window still in future)", st.Status)
	}
	if st.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0 (used 1/1)", st.Remaining)
	}
	if st.UsedBytes != 4_000_000_000 {
		t.Fatalf("usedBytes = %d, want 4 GB (5GB lifetime - 1GB baseline)", st.UsedBytes)
	}
}

func TestEmergencyStatus_NoQuotaOnlyWhenWindowExpired(t *testing.T) {
	now := time.Now()
	past := now.Add(-1 * time.Hour) // window expired
	u := &domain.User{
		ID:                 1,
		Enabled:            true,
		EmergencyUsedCount: 1,
		EmergencyUntil:     &past,
	}
	st := EmergencyAccessStatusForUserWithTrafficLimit(u, emSettings(), now, false)
	if st.Status != "no_quota" {
		t.Fatalf("status = %q, want no_quota (window expired AND remaining=0)", st.Status)
	}
}

func TestEmergencyStatus_AvailableWhenTrafficExceeded(t *testing.T) {
	now := time.Now()
	u := &domain.User{
		ID:                 1,
		Enabled:            true,
		EmergencyUsedCount: 0,
	}
	st := EmergencyAccessStatusForUserWithTrafficLimit(u, emSettings(), now, true) // trafficLimitExceeded=true
	if st.Status != "available" {
		t.Fatalf("status = %q, want available", st.Status)
	}
	if !st.Available {
		t.Fatal("Available must be true for status=available")
	}
}

func TestEmergencyStatus_NotEligibleWhenNothingWrong(t *testing.T) {
	now := time.Now()
	u := &domain.User{
		ID:                 1,
		Enabled:            true,
		EmergencyUsedCount: 0,
	}
	st := EmergencyAccessStatusForUserWithTrafficLimit(u, emSettings(), now, false)
	if st.Status != "not_eligible" {
		t.Fatalf("status = %q, want not_eligible (no expiry, no traffic exceeded)", st.Status)
	}
	if st.Available {
		t.Fatal("Available must be false when not eligible")
	}
}

func TestEmergencyStatus_DisabledWhenSettingsOff(t *testing.T) {
	s := emSettings()
	s.EmergencyAccessEnabled = false
	st := EmergencyAccessStatusForUserWithTrafficLimit(&domain.User{ID: 1}, s, time.Now(), false)
	if st.Status != "disabled" {
		t.Fatalf("status = %q, want disabled", st.Status)
	}
}

func TestEmergencyStatus_InvalidSettings(t *testing.T) {
	s := emSettings()
	s.EmergencyAccessHours = 0 // invalid
	st := EmergencyAccessStatusForUserWithTrafficLimit(&domain.User{ID: 1}, s, time.Now(), false)
	if st.Status != "invalid_settings" {
		t.Fatalf("status = %q, want invalid_settings", st.Status)
	}
}

func TestEmergencyStatus_UserNotFound(t *testing.T) {
	st := EmergencyAccessStatusForUserWithTrafficLimit(nil, emSettings(), time.Now(), false)
	if st.Status != "user_not_found" {
		t.Fatalf("status = %q, want user_not_found", st.Status)
	}
}

func TestEmergencyStatus_ExpiredAccountIsEligible(t *testing.T) {
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	u := &domain.User{
		ID:       1,
		Enabled:  true,
		ExpireAt: &yesterday,
	}
	st := EmergencyAccessStatusForUserWithTrafficLimit(u, emSettings(), now, false)
	if st.Status != "available" {
		t.Fatalf("status = %q, want available (expired account)", st.Status)
	}
}

func TestEmergencyStatus_QuotaBytesReflectsSettings(t *testing.T) {
	s := emSettings()
	s.EmergencyAccessQuotaGB = 5
	st := EmergencyAccessStatusForUserWithTrafficLimit(&domain.User{ID: 1, Enabled: true}, s, time.Now(), false)
	wantQuota := int64(5) * 1024 * 1024 * 1024
	if st.QuotaBytes != wantQuota {
		t.Fatalf("quotaBytes = %d, want %d", st.QuotaBytes, wantQuota)
	}
}

func TestEmergencyStatus_UsedBytesZeroWhenNoActiveWindow(t *testing.T) {
	now := time.Now()
	u := &domain.User{
		ID:                     1,
		Enabled:                true,
		LifetimeTotalBytes:     5_000_000_000,
		EmergencyBaselineBytes: 1_000_000_000, // stale baseline from a previous window
		// EmergencyUntil = nil
	}
	st := EmergencyAccessStatusForUserWithTrafficLimit(u, emSettings(), now, false)
	if st.UsedBytes != 0 {
		t.Fatalf("usedBytes = %d, want 0 (no active window, baseline must be ignored)", st.UsedBytes)
	}
}

// The SSO role-policy unit tests live in the auth package now
// (see internal/service/auth/role_test.go) because the policy moved
// out of this package — user.EnsureSSO just calls auth.ResolveRoleForSSO.

func TestEnsureSSO_FirstLoginPersistsLocalAccountBinding(t *testing.T) {
	ctx := context.Background()
	repo := &memoryUserRepo{
		byID: map[int64]*domain.User{
			2: {
				ID:          2,
				UPN:         "me@kazuha.org",
				Role:        domain.RoleAdmin,
				SSOProvider: domain.SSOProviderLocal,
				SSOSubject:  "me@kazuha.org",
				Enabled:     true,
			},
		},
	}
	svc := &Service{users: repo}

	u, err := svc.EnsureSSO(ctx, EnsureSSOInput{
		Provider: domain.SSOProviderSAML,
		Subject:  "entra-subject-123",
		UPN:      "me@kazuha.org",
		Email:    "me@kazuha.org",
	})
	if err != nil {
		t.Fatalf("EnsureSSO returned error: %v", err)
	}
	if u.SSOProvider != domain.SSOProviderSAML || u.SSOSubject != "entra-subject-123" {
		t.Fatalf("returned binding = (%q, %q), want (%q, %q)",
			u.SSOProvider, u.SSOSubject, domain.SSOProviderSAML, "entra-subject-123")
	}
	if repo.updateCalls != 1 {
		t.Fatalf("Update calls = %d, want 1", repo.updateCalls)
	}
	stored := repo.byID[2]
	if stored.SSOProvider != domain.SSOProviderSAML || stored.SSOSubject != "entra-subject-123" {
		t.Fatalf("stored binding = (%q, %q), want (%q, %q)",
			stored.SSOProvider, stored.SSOSubject, domain.SSOProviderSAML, "entra-subject-123")
	}
}

type memoryUserRepo struct {
	byID        map[int64]*domain.User
	updateCalls int
}

func (r *memoryUserRepo) Create(ctx context.Context, u *domain.User) error {
	if r.byID == nil {
		r.byID = map[int64]*domain.User{}
	}
	if u.ID == 0 {
		u.ID = int64(len(r.byID) + 1)
	}
	r.byID[u.ID] = cloneUser(u)
	return nil
}

func (r *memoryUserRepo) Update(ctx context.Context, u *domain.User) error {
	r.updateCalls++
	r.byID[u.ID] = cloneUser(u)
	return nil
}

func (r *memoryUserRepo) UpdateTrafficState(ctx context.Context, u *domain.User) error {
	cur, ok := r.byID[u.ID]
	if !ok {
		return nil
	}
	cur.LifetimeUpBytes = u.LifetimeUpBytes
	cur.LifetimeDownBytes = u.LifetimeDownBytes
	cur.LifetimeTotalBytes = u.LifetimeTotalBytes
	cur.PeriodBaselineBytes = u.PeriodBaselineBytes
	cur.LifetimeBaselineAt = u.LifetimeBaselineAt
	cur.TrafficPeriodStart = u.TrafficPeriodStart
	return nil
}

func (r *memoryUserRepo) ClearEmergencyAccess(ctx context.Context, userID int64) error {
	if cur, ok := r.byID[userID]; ok {
		cur.EmergencyUntil = nil
		cur.EmergencyBaselineBytes = 0
	}
	return nil
}

func (r *memoryUserRepo) Delete(ctx context.Context, id int64) error {
	delete(r.byID, id)
	return nil
}

func (r *memoryUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	if u := r.byID[id]; u != nil {
		return cloneUser(u), nil
	}
	return nil, domain.ErrNotFound
}

func (r *memoryUserRepo) GetByUPN(ctx context.Context, upn string) (*domain.User, error) {
	for _, u := range r.byID {
		if u.UPN == upn {
			return cloneUser(u), nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *memoryUserRepo) GetBySSO(ctx context.Context, provider, subject string) (*domain.User, error) {
	for _, u := range r.byID {
		if u.SSOProvider == provider && u.SSOSubject == subject {
			return cloneUser(u), nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *memoryUserRepo) GetBySubToken(ctx context.Context, token string) (*domain.User, error) {
	return nil, domain.ErrNotFound
}

func (r *memoryUserRepo) List(ctx context.Context, filter ports.UserFilter) ([]*domain.User, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (r *memoryUserRepo) ListByGroup(ctx context.Context, groupID int64) ([]*domain.User, error) {
	return nil, errors.New("not implemented")
}

func cloneUser(u *domain.User) *domain.User {
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}
