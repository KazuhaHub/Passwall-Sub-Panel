package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// The group rules are a new JSON column on two existing single-row tables, so
// two things can go wrong that no unit test upstream would notice: the column
// may not be created at all on an EXISTING install (AutoMigrate has to add it
// to a table that predates it), and the encode/decode pair may not round-trip.
//
// Either failure is invisible until runtime and then total — an admin saves
// rules, the form reports success, and every login behaves as if the rule set
// were empty, which is exactly the "no rules configured" state that means
// "leave everyone alone". A silently empty rule set is indistinguishable from
// a deliberately empty one.

func ssoRules() []config.SSOGroupRule {
	return []config.SSOGroupRule{
		{Attribute: "", Value: "idp-vip", Group: "vip", Keep: true, Note: "premium tier"},
		{Attribute: "department", Value: "finance", Group: "staff"},
	}
}

func assertRulesEqual(t *testing.T, got, want []config.SSOGroupRule) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("round trip changed the rule count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			// Compared field by field on purpose: a dropped Keep or Note
			// would still leave a plausible-looking rule list.
			t.Fatalf("rule %d round-tripped as %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSAMLGroupRulesRoundTrip(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &samlConfigRepo{db: db}
	ctx := context.Background()

	cfg, err := repo.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.GroupRules = ssoRules()
	if err := repo.Save(ctx, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	back, err := repo.Load(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	assertRulesEqual(t, back.GroupRules, ssoRules())

	// Clearing must persist as "no rules", not as "unchanged". Handing an
	// OU back to manual management is done by deleting its rule, and a
	// clear that silently kept the old list would go on demoting people.
	back.GroupRules = nil
	if err := repo.Save(ctx, back); err != nil {
		t.Fatalf("save cleared: %v", err)
	}
	cleared, err := repo.Load(ctx)
	if err != nil {
		t.Fatalf("reload cleared: %v", err)
	}
	if len(cleared.GroupRules) != 0 {
		t.Fatalf("cleared rules came back as %+v", cleared.GroupRules)
	}
}

func TestOIDCGroupRulesRoundTrip(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &oidcConfigRepo{db: db}
	ctx := context.Background()

	cfg, err := repo.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.GroupRules = ssoRules()
	if err := repo.Save(ctx, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := repo.Load(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	assertRulesEqual(t, back.GroupRules, ssoRules())
}

// The upgrade path specifically: a row written by a build that had no
// group_rules column reads back as an empty rule set rather than failing to
// scan. EnsureSchema running twice models the "old install, new binary" order
// closely enough to catch a decoder that cannot handle the default.
func TestGroupRulesAbsentColumnDecodesAsEmpty(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	repo := &samlConfigRepo{db: db}
	ctx := context.Background()

	// Null the column the way a pre-upgrade row would leave it.
	if err := db.Model(&samlConfigRow{}).Where("id = 1").
		UpdateColumn("group_rules", nil).Error; err != nil {
		t.Fatalf("null the column: %v", err)
	}
	cfg, err := repo.Load(ctx)
	if err != nil {
		t.Fatalf("a row predating the column must still load: %v", err)
	}
	if len(cfg.GroupRules) != 0 {
		t.Fatalf("null column decoded as %+v, want no rules", cfg.GroupRules)
	}
}
