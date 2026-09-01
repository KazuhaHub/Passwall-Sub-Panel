package sqlstore

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func i64p(v int64) *int64 { return &v }
func intp(v int) *int     { return &v }

// reposFor builds the same wiring production uses, so the group-limits cache is
// shared between the two repos exactly as it is at runtime. A test that
// hand-built &userRepo{db: db} would silently resolve every user to unlimited
// and prove nothing.
func reposFor(t *testing.T, db *gorm.DB) (*userRepo, *groupRepo) {
	t.Helper()
	cache := newGroupLimitsCache(db)
	return &userRepo{db: db, groupLimits: cache}, &groupRepo{db: db, limitsCache: cache}
}

func seedGroup(t *testing.T, gr *groupRepo, slug string, lim domain.GroupLimits) *domain.Group {
	t.Helper()
	g := &domain.Group{Slug: slug, Name: slug, Limits: lim}
	if err := gr.Create(context.Background(), g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	return g
}

func seedUser(t *testing.T, ur *userRepo, upn string, groupID int64, lim domain.LimitOverrides) *domain.User {
	t.Helper()
	u := &domain.User{
		UPN: upn, SubToken: upn + "-tok", UUID: upn + "-uuid",
		Role: domain.RoleUser, GroupID: groupID, Enabled: true, Limits: lim,
	}
	if err := ur.Create(context.Background(), u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

// The end-to-end promise: a user with no override of their own takes the
// group's entitlements, and one with an override keeps it.
func TestLimitsInheritFromGroup(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ur, gr := reposFor(t, db)
	ctx := context.Background()

	g := seedGroup(t, gr, "premium", domain.GroupLimits{
		TrafficLimitBytes: i64p(100 << 30), IPLimit: intp(3), DeviceLimit: intp(2),
	})

	t.Run("an all-nil user inherits every field", func(t *testing.T) {
		u := seedUser(t, ur, "inheritor", g.ID, domain.LimitOverrides{})
		got, err := ur.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TrafficLimitBytes != 100<<30 || got.IPLimit != 3 || got.DeviceLimit != 2 {
			t.Fatalf("resolved = %d/%d/%d, want the group's 107374182400/3/2",
				got.TrafficLimitBytes, got.IPLimit, got.DeviceLimit)
		}
		if !got.Limits.InheritsTrafficLimit() || !got.Limits.InheritsIPLimit() || !got.Limits.InheritsDeviceLimit() {
			t.Fatal("the stored overrides must still read as inheriting")
		}
	})

	t.Run("an explicit override wins and survives the round trip", func(t *testing.T) {
		u := seedUser(t, ur, "overrider", g.ID, domain.LimitOverrides{IPLimit: intp(9)})
		got, err := ur.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.IPLimit != 9 {
			t.Fatalf("IPLimit = %d, want the user's 9", got.IPLimit)
		}
		// The other two still inherit — the fields resolve independently.
		if got.TrafficLimitBytes != 100<<30 || got.DeviceLimit != 2 {
			t.Fatalf("traffic/device = %d/%d, want the group's", got.TrafficLimitBytes, got.DeviceLimit)
		}
	})

	// The case the third state exists for. Without it this user would silently
	// pick up the group's caps.
	t.Run("an explicit zero means unlimited, not inherit", func(t *testing.T) {
		u := seedUser(t, ur, "uncapped", g.ID, domain.LimitOverrides{
			TrafficLimitBytes: i64p(0), IPLimit: intp(0), DeviceLimit: intp(0),
		})
		got, err := ur.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TrafficLimitBytes != 0 || got.IPLimit != 0 || got.DeviceLimit != 0 {
			t.Fatalf("resolved = %d/%d/%d, want all-unlimited",
				got.TrafficLimitBytes, got.IPLimit, got.DeviceLimit)
		}
		if got.Limits.InheritsIPLimit() {
			t.Fatal("an explicit zero must not read as inheriting")
		}
	})
}

// Editing a group's policy must reach its members at once, not after the cache
// TTL. The two repos share one cache precisely so a write can invalidate it.
func TestGroupLimitEditReachesMembersImmediately(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ur, gr := reposFor(t, db)
	ctx := context.Background()

	g := seedGroup(t, gr, "starter", domain.GroupLimits{IPLimit: intp(1)})
	u := seedUser(t, ur, "member", g.ID, domain.LimitOverrides{})

	got, err := ur.GetByID(ctx, u.ID) // warms the cache
	if err != nil || got.IPLimit != 1 {
		t.Fatalf("precondition: IPLimit = %v (err %v), want 1", got, err)
	}

	g.Limits.IPLimit = intp(7)
	if err := gr.Update(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, err = ur.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IPLimit != 7 {
		t.Fatalf("IPLimit = %d, want 7 — the group write did not invalidate the cache", got.IPLimit)
	}
}

// A user whose group was deleted must keep working, resolving to unlimited
// rather than erroring or holding a stale cap.
func TestLimitsSurviveGroupDeletion(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ur, gr := reposFor(t, db)
	ctx := context.Background()

	g := seedGroup(t, gr, "doomed", domain.GroupLimits{IPLimit: intp(4)})
	u := seedUser(t, ur, "orphan", g.ID, domain.LimitOverrides{})
	if got, _ := ur.GetByID(ctx, u.ID); got.IPLimit != 4 {
		t.Fatalf("precondition: IPLimit = %d, want 4", got.IPLimit)
	}
	if err := gr.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	got, err := ur.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("a user whose group is gone must still load: %v", err)
	}
	if got.IPLimit != 0 {
		t.Fatalf("IPLimit = %d, want 0 (unlimited) after the group vanished", got.IPLimit)
	}
}
