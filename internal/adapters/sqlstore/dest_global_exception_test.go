package sqlstore

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"gorm.io/gorm"
)

func globalExceptionEdit(entry string) func(domain.DestList) (domain.DestList, error) {
	return func(list domain.DestList) (domain.DestList, error) {
		for _, old := range strings.Split(string(list.Entries), "\n") {
			if old == entry {
				return list, nil
			}
		}
		return destlist.PrepareEntryPatch(list, true, []string{entry}, nil)
	}
}

func TestDestinationGlobalExceptionLateFailureRollsBackListsPolicyOrderAndVersions(t *testing.T) {
	for _, table := range []string{"dest_policies", "dest_policy_state"} {
		t.Run(table, func(t *testing.T) {
			r := newDestDefinitionRepo(t)
			now := time.UnixMilli(1791000000000).UTC()
			original := destinationTestPolicy("放行例外")
			original.Action = domain.DestAllow
			if err := r.SavePolicy(t.Context(), &original, time.Time{}, now); err != nil {
				t.Fatal(err)
			}
			problem := errors.New("global exception late failure")
			callback := "exception-failure"
			if table == "dest_policies" {
				if err := r.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(problem)
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.db.Callback().Create().Remove(callback) })
			} else {
				if err := r.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(problem)
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = r.db.Callback().Update().Remove(callback) })
			}
			result, err := r.AddGlobalException(t.Context(), now, globalExceptionEdit("domain:example.test"))
			if !errors.Is(err, problem) || result != (domain.DestGlobalExceptionCommit{}) {
				t.Fatal("failed bundle returned partial creation identity")
			}
			defs, err := r.ReadDefinitions(t.Context())
			if err != nil || defs.State.Generation != 1 || len(defs.Lists) != 0 || len(defs.Policies) != 1 || defs.Policies[0].Priority != original.Priority || !defs.Policies[0].UpdatedAt.Equal(original.UpdatedAt) {
				t.Fatal("failed bundle changed definitions/order/version")
			}
		})
	}
}

func TestDestinationGlobalExceptionConcurrentFirstUseHasOneBundleAndSafeNameCollision(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("放行例外")
	p.Action = domain.DestAllow
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan domain.DestGlobalExceptionCommit, 2)
	errs := make(chan error, 2)
	for _, entry := range []string{"full:one.example.test", "full:two.example.test"} {
		wg.Go(func() {
			result, err := r.AddGlobalException(t.Context(), now, globalExceptionEdit(entry))
			results <- result
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	created := 0
	var listID, policyID int64
	for result := range results {
		if result.Created {
			created++
		}
		if listID != 0 && (listID != result.ListID || policyID != result.PolicyID) {
			t.Fatal("concurrent calls created separate bundles")
		}
		listID, policyID = result.ListID, result.PolicyID
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || created != 1 || defs.State.Generation != 3 || len(defs.Lists) != 1 || defs.Lists[0].EntryCount != 2 || len(defs.Policies) != 2 {
		t.Fatal("concurrent first use lost content or created orphans")
	}
	for _, policy := range defs.Policies {
		if policy.ID == policyID && (policy.Name == p.Name || policy.Priority != 1 || policy.TemplateKey != domain.DestGlobalExceptionTemplateKey) {
			t.Fatal("bundle adopted a same-name user policy or lost identity")
		}
	}
}
