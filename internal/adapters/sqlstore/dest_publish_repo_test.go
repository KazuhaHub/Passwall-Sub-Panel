package sqlstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The reads the policy compiler adds to the destination store family:
//
//   - ReadDefinitions: every definition and the generation it was taken at,
//     in one consistent read (a REPEATABLE READ read-only transaction on
//     PostgreSQL and MySQL, where READ COMMITTED would let a write land
//     between the generation read and the definitions read);
//   - GroupIDsByUserIDs: the user -> group map the compiler filters a
//     group-scoped policy by;
//   - GetMeta / AppliedBody / List on the agent rows, so the large LKG body is
//     only read when a fallback needs it;
//   - the action change of a policy moves it to the end of its new action.

func seedDestFixture(t *testing.T, repos *DestRepos, ctx context.Context) (list *domain.DestList, policy *domain.DestPolicy) {
	t.Helper()
	list = &domain.DestList{
		Name: "bt", Kind: domain.DestListCustom, Side: domain.DestSideDest,
		Entries: []byte("domain:example.com\nfull:a.example.net"), SourceText: []byte("DOMAIN-SUFFIX,example.com"),
		EntryCount: 2, ContentSHA256: strings.Repeat("a", 64),
	}
	if err := repos.Lists.Create(ctx, list); err != nil {
		t.Fatal(err)
	}
	policy = &domain.DestPolicy{
		Name: "block bt", Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll,
		ListIDs: []int64{list.ID},
	}
	if err := repos.Policies.Create(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return list, policy
}

func TestDestReadDefinitions_ReturnsEverythingAtOneGeneration(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	list, policy := seedDestFixture(t, repos, ctx)
	if err := repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: 7, Reason: "ops"}); err != nil {
		t.Fatal(err)
	}
	if err := repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 3, Countries: []string{"CN"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	want := destGeneration(t, repos, ctx)

	defs, err := repos.ReadDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defs.Generation != want {
		t.Fatalf("generation = %d, want %d", defs.Generation, want)
	}
	if len(defs.Lists) != 1 || defs.Lists[0].ID != list.ID || string(defs.Lists[0].Entries) != string(list.Entries) {
		t.Fatalf("lists = %+v (entries must be present)", defs.Lists)
	}
	if len(defs.Lists[0].SourceText) != 0 {
		t.Fatal("the pasted text is not a definition the compiler reads; it must not be loaded")
	}
	if len(defs.Policies) != 1 || defs.Policies[0].ID != policy.ID {
		t.Fatalf("policies = %+v", defs.Policies)
	}
	if len(defs.Exemptions) != 1 || defs.Exemptions[0].UserID != 7 {
		t.Fatalf("exemptions = %+v", defs.Exemptions)
	}
	if len(defs.SourceRules) != 1 || defs.SourceRules[0].NodeID != 3 || defs.SourceRules[0].Countries[0] != "CN" {
		t.Fatalf("source rules = %+v", defs.SourceRules)
	}
}

func TestDestReadDefinitions_EmptyInstallIsGenerationZero(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	defs, err := repos.ReadDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defs.Generation != 0 || len(defs.Lists)+len(defs.Policies)+len(defs.Exemptions)+len(defs.SourceRules) != 0 {
		t.Fatalf("fresh install read = %+v", defs)
	}
}

// The read must run in the transaction mode the plan calls for on the server
// dialects. SQLite has one connection and is consistent by construction, so
// the isolation is asserted by what the repo asks the database for, on every
// dialect.
func TestDestReadDefinitions_AsksForARepeatableReadReadOnlyTransactionOnServerDialects(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	var mu sync.Mutex
	var statements []string
	if err := db.Callback().Query().Before("gorm:query").Register("test:capture", func(tx *gorm.DB) {
		mu.Lock()
		defer mu.Unlock()
		statements = append(statements, tx.Statement.Table)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.ReadDefinitions(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// The generation comes first, then the definitions.
	if len(statements) < 5 || statements[0] != "dest_policy_state" {
		t.Fatalf("read order = %v, the state row (the generation) must be read first", statements)
	}
	if got := destReadOptions(db); db.Dialector.Name() == "sqlite" {
		if got != nil {
			t.Fatal("SQLite must not be handed transaction options its driver may refuse")
		}
	} else if got == nil || !got.ReadOnly || got.Isolation.String() != "Repeatable Read" {
		t.Fatalf("server dialect options = %+v, want a repeatable-read read-only transaction", got)
	}
}

func TestDestGroupIDsByUserIDs(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	for _, u := range []userRow{
		{UPN: "a@x", SubToken: "tok-a", UUID: "u-a", GroupID: 11},
		{UPN: "b@x", SubToken: "tok-b", UUID: "u-b", GroupID: 12},
		{UPN: "c@x", SubToken: "tok-c", UUID: "u-c", GroupID: 11},
	} {
		u := u
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	if err := db.Model(&userRow{}).Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	got, err := repos.GroupIDsByUserIDs(ctx, append(ids, 9999))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[ids[0]] != 11 || got[ids[1]] != 12 || got[ids[2]] != 11 {
		t.Fatalf("groups = %v", got)
	}
	if got, err := repos.GroupIDsByUserIDs(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("no ids = (%v, %v)", got, err)
	}
}

func TestDestCountUsersByGroup(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	for _, u := range []userRow{
		{UPN: "a@x", SubToken: "t1", UUID: "u", GroupID: 11},
		{UPN: "b@x", SubToken: "t2", UUID: "u", GroupID: 12},
		{UPN: "c@x", SubToken: "t3", UUID: "u", GroupID: 11},
	} {
		u := u
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := repos.CountUsersByGroup(ctx)
	if err != nil || got[11] != 2 || got[12] != 1 || len(got) != 2 {
		t.Fatalf("counts = %v (%v)", got, err)
	}
}

// 50 000 roster users must not become one IN list the database refuses.
func TestDestGroupIDsByUserIDs_ChunksALargeRoster(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	rows := make([]userRow, 0, 1200)
	for i := 0; i < 1200; i++ {
		rows = append(rows, userRow{UPN: "u" + time.Duration(i).String(), SubToken: "t" + time.Duration(i).String(), UUID: "x", GroupID: int64(1 + i%3)})
	}
	if err := db.CreateInBatches(&rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	got, err := repos.GroupIDsByUserIDs(ctx, ids)
	if err != nil || len(got) != 1200 {
		t.Fatalf("got %d groups, err %v", len(got), err)
	}
}

func TestDestAgentPolicy_MetaLeavesTheBodyAndListFindsEveryRow(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	body := []byte(strings.Repeat("x", 4096))
	caps := "ph"
	applied := strings.Repeat("c", 64)
	if err := repos.Agents.Patch(ctx, "agt_a", domain.DestAgentPolicyPatch{AppliedSHA256: &applied, AppliedBody: &body, RejectedCaps: &caps}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Agents.Patch(ctx, "agt_b", domain.DestAgentPolicyPatch{}); err != nil {
		t.Fatal(err)
	}
	meta, err := repos.Agents.GetMeta(ctx, "agt_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.AppliedBody) != 0 || meta.AppliedSHA256 != applied || meta.RejectedCaps != "ph" {
		t.Fatalf("meta = %+v: the LKG body must stay in the database", meta)
	}
	got, err := repos.Agents.AppliedBody(ctx, "agt_a")
	if err != nil || string(got) != string(body) {
		t.Fatalf("applied body = (%d bytes, %v)", len(got), err)
	}
	if _, err := repos.Agents.GetMeta(ctx, "agt_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent meta = %v, want ErrNotFound", err)
	}
	if got, err := repos.Agents.AppliedBody(ctx, "agt_missing"); err != nil || got != nil {
		t.Fatalf("missing agent body = (%v, %v), want nothing and no error", got, err)
	}
	all, err := repos.Agents.List(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("list = (%d rows, %v)", len(all), err)
	}
	for _, r := range all {
		if len(r.AppliedBody) != 0 {
			t.Fatal("List must not load the LKG bodies")
		}
	}
}

func TestDestPolicyUpdate_ChangingTheActionMovesItToTheEndOfTheNewAction(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	mk := func(name string, a domain.DestAction) *domain.DestPolicy {
		p := &domain.DestPolicy{Name: name, Action: a, State: domain.DestStateOn, Scope: domain.DestScopeAll,
			Inline: domain.DestPolicyInline{Ports: "25"}}
		if err := repos.Policies.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	b1, b2, a1 := mk("b1", domain.DestActionBlock), mk("b2", domain.DestActionBlock), mk("a1", domain.DestActionAllow)
	if b1.Priority != 1 || b2.Priority != 2 || a1.Priority != 1 {
		t.Fatalf("priorities = %d %d %d", b1.Priority, b2.Priority, a1.Priority)
	}
	b1.Action = domain.DestActionAllow
	if err := repos.Policies.Update(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	if b1.Priority != 2 {
		t.Fatalf("after moving to allow, priority = %d, want 2 (last of allow)", b1.Priority)
	}
	// Same action: priority untouched.
	b2.Description = "edited"
	if err := repos.Policies.Update(ctx, b2, nil); err != nil || b2.Priority != 2 {
		t.Fatalf("same-action update priority = %d, err %v", b2.Priority, err)
	}
}

// The equal-content path of MintStream is the one that runs every thirty
// seconds for every node: it must read the identity columns only. A config
// body carrying a multi-MiB policy would otherwise be read in full each time.
func TestNodeAgentMintStream_EqualPathDoesNotReadTheBody(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewRepos(db).NodeAgent
	agent := &domain.NodeAgent{AgentID: "agt_eq", PanelID: 21, CredentialSHA256: strings.Repeat("ab", 32)}
	if err := repo.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"listeners":[],"coverage":{"entries":0},"core":{}}`)
	first, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig, body, now)
	if err != nil || !minted {
		t.Fatalf("first mint = (%v, %v)", minted, err)
	}

	var mu sync.Mutex
	var sqls []string
	if err := db.Callback().Query().After("gorm:query").Register("test:capture_sql", func(tx *gorm.DB) {
		mu.Lock()
		defer mu.Unlock()
		sqls = append(sqls, tx.Statement.SQL.String())
	}); err != nil {
		t.Fatal(err)
	}
	second, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig, body, now.Add(time.Minute))
	if err != nil || minted {
		t.Fatalf("equal mint = (%v, %v)", minted, err)
	}
	if second.DesiredVersion != first.DesiredVersion || second.DesiredETag != first.DesiredETag {
		t.Fatalf("equal mint advanced identity: %+v vs %+v", second, first)
	}
	mu.Lock()
	defer mu.Unlock()
	seen := 0
	for _, q := range sqls {
		if !strings.Contains(q, "node_agent_streams") {
			continue
		}
		seen++
		// GORM selects every column (`*`) unless told which: the narrow read
		// names the identity columns and never the body.
		if strings.Contains(q, "*") || strings.Contains(q, "desired_body") || !strings.Contains(q, "desired_etag") {
			t.Fatalf("equal path read more than the identity columns: %s", q)
		}
	}
	if seen == 0 {
		t.Fatal("no stream query captured; the test proves nothing")
	}
}

// An account's exemption leaves with the account, in the same transaction, and
// the removal is a definition change (the generation moves).
func TestDestExemptionLeavesWithItsUser(t *testing.T) {
	repos, db, ctx := newDestRepos(t)
	u := userRow{UPN: "gone@x", SubToken: "tok-gone", UUID: "u", GroupID: 1}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := repos.Exemptions.Upsert(ctx, &domain.DestExemption{UserID: u.ID, Reason: "ops"}); err != nil {
		t.Fatal(err)
	}
	before := destGeneration(t, repos, ctx)
	if err := NewRepos(db).User.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Exemptions.Get(ctx, u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the exemption of a deleted user remained: %v", err)
	}
	if after := destGeneration(t, repos, ctx); after != before+1 {
		t.Fatalf("generation %d -> %d: removing an exemption in force is a definition change", before, after)
	}
	// A user with no exemption: no generation churn.
	u2 := userRow{UPN: "other@x", SubToken: "tok-other", UUID: "u", GroupID: 1}
	if err := db.Create(&u2).Error; err != nil {
		t.Fatal(err)
	}
	g := destGeneration(t, repos, ctx)
	if err := NewRepos(db).User.Delete(ctx, u2.ID); err != nil {
		t.Fatal(err)
	}
	if destGeneration(t, repos, ctx) != g {
		t.Fatal("deleting a user with no exemption moved the generation")
	}
}
