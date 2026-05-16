package user

import (
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
		ID:                 1,
		Enabled:            true,
		EmergencyUsedCount: 1, // used it once
		EmergencyUntil:     &until,
		LifetimeTotalBytes: 5_000_000_000,
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

// Promote-only invariant: every SSO login goes through applyRoleFromSSO,
// which must allow IdP to RAISE a non-admin to admin but never DEMOTE
// an existing role. Without this guarantee a panel admin can't hand out
// the operator role manually without it getting washed back to user on
// the next SSO bounce.

func TestApplyRoleFromSSO_PromotesUserToAdmin(t *testing.T) {
	role, changed := applyRoleFromSSO(domain.RoleUser, true)
	if role != domain.RoleAdmin || !changed {
		t.Fatalf("user + IdP admin: got (%q, %v), want (admin, true)", role, changed)
	}
}

func TestApplyRoleFromSSO_KeepsAdminWhenIdPStillAdmin(t *testing.T) {
	// No-op fast path — should not trigger a DB write.
	role, changed := applyRoleFromSSO(domain.RoleAdmin, true)
	if role != domain.RoleAdmin || changed {
		t.Fatalf("admin + IdP admin: got (%q, %v), want (admin, false)", role, changed)
	}
}

func TestApplyRoleFromSSO_DoesNotDemoteAdminWhenIdPMisses(t *testing.T) {
	// The whole point: IdP says "not admin" but the panel admin already
	// promoted this account. Must NOT clobber.
	role, changed := applyRoleFromSSO(domain.RoleAdmin, false)
	if role != domain.RoleAdmin || changed {
		t.Fatalf("admin + IdP miss: got (%q, %v), want (admin, false) — must not demote", role, changed)
	}
}

func TestApplyRoleFromSSO_DoesNotWashOperatorToUser(t *testing.T) {
	// The operator role lives entirely in panel-land — the IdP doesn't
	// have a concept of it. SSO logins must leave it alone.
	role, changed := applyRoleFromSSO(domain.RoleOperator, false)
	if role != domain.RoleOperator || changed {
		t.Fatalf("operator + IdP miss: got (%q, %v), want (operator, false)", role, changed)
	}
}

func TestApplyRoleFromSSO_PromotesOperatorWhenIdPSaysAdmin(t *testing.T) {
	// If a panel-granted operator later shows up in the IdP admin group,
	// promotion to admin is fine — they qualify on both sides now.
	role, changed := applyRoleFromSSO(domain.RoleOperator, true)
	if role != domain.RoleAdmin || !changed {
		t.Fatalf("operator + IdP admin: got (%q, %v), want (admin, true)", role, changed)
	}
}

func TestApplyRoleFromSSO_UserUnchangedWhenIdPMisses(t *testing.T) {
	// Regular SSO login by a non-admin — most common case, must be a
	// no-op so we don't burn an Update round-trip every login.
	role, changed := applyRoleFromSSO(domain.RoleUser, false)
	if role != domain.RoleUser || changed {
		t.Fatalf("user + IdP miss: got (%q, %v), want (user, false)", role, changed)
	}
}
