package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"gorm.io/gorm"
)

type pairedDestinationWriter interface {
	SavePolicyWithList(context.Context, *domain.DestPolicy, *domain.DestList, time.Time) error
}

func pairedDestinationRepo(t *testing.T, repo *DestDefinitionRepo) pairedDestinationWriter {
	t.Helper()
	writer, ok := any(repo).(pairedDestinationWriter)
	if !ok {
		t.Fatal("destination repository cannot atomically save a template policy and list")
	}
	return writer
}

func templateDefinitionList(t *testing.T) domain.DestList {
	t.Helper()
	parsed, err := destlist.ParseCustom([]byte("domain:exchange.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1791000000000).UTC()
	return domain.DestList{Name: "Exchange category", Kind: domain.DestListGeosite, GeositeCategory: "category-cryptocurrency", Entries: parsed.Entries, EntryCount: parsed.EntryCount, RegexpCount: parsed.RegexpCount, ContentSHA256: parsed.ContentSHA256, ParseReport: &parsed.Report, LastFetchedAt: &at}
}

func TestDestinationTemplateCommitsOneGenerationAndRealListIdentity(t *testing.T) {
	r := newDestDefinitionRepo(t)
	writer := pairedDestinationRepo(t, r)
	list := templateDefinitionList(t)
	policy := domain.DestPolicy{Name: "Exchanges", Action: domain.DestObserve, Scope: domain.DestScopeAll, Enabled: true, TemplateKey: "crypto"}
	at := time.UnixMilli(1791000000123).UTC()
	if err := writer.SavePolicyWithList(t.Context(), &policy, &list, at); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 1 || len(defs.Lists) != 1 || len(defs.Policies) != 1 {
		t.Fatalf("paired generation/read: %+v / %v", defs, err)
	}
	if list.ID <= 0 || policy.ID <= 0 || len(policy.ListIDs) != 1 || policy.ListIDs[0] != list.ID || !policy.UpdatedAt.Equal(at) || !list.UpdatedAt.Equal(at) || policy.Priority != 1 || policy.Action != domain.DestObserve || policy.CountsAsRisk {
		t.Fatalf("pair identity/defaults changed: %+v / %+v", policy, list)
	}
	if defs.Lists[0].ContentSHA256 != list.ContentSHA256 || defs.Lists[0].ParseReport == nil || defs.Lists[0].GeositeCategory != "category-cryptocurrency" {
		t.Fatal("paired write lost category provenance")
	}
}

func TestDestinationTemplateFailuresRollbackListPolicyGenerationAndCaller(t *testing.T) {
	for _, reason := range []string{"policy-name", "list-write", "generation", "invalid-policy", "missing-reference", "future-reference", "quota", "canceled"} {
		t.Run(reason, func(t *testing.T) {
			r := newDestDefinitionRepo(t)
			writer := pairedDestinationRepo(t, r)
			list := templateDefinitionList(t)
			policy := domain.DestPolicy{Name: "Exchanges", Action: domain.DestObserve, Scope: domain.DestScopeAll, Enabled: true, TemplateKey: "crypto"}
			at := time.UnixMilli(1791000000123).UTC()
			ctx := t.Context()
			switch reason {
			case "policy-name":
				old := destinationTestPolicy(policy.Name)
				if err := r.SavePolicy(ctx, &old, time.Time{}, at); err != nil {
					t.Fatal(err)
				}
			case "list-write":
				if err := r.db.Callback().Create().Before("gorm:create").Register("template-list-fault", func(tx *gorm.DB) {
					if tx.Statement.Table == "dest_lists" {
						tx.AddError(errors.New("injected category write failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			case "generation":
				if err := r.db.Create(&destPolicyStateRow{ID: 1, Generation: math.MaxInt64}).Error; err != nil {
					t.Fatal(err)
				}
			case "invalid-policy":
				policy.Inline.Ports = "65536"
			case "missing-reference":
				policy.ListIDs = []int64{99}
			case "future-reference":
				policy.ListIDs = []int64{1}
			case "quota":
				rows := make([]destPolicyRow, protocol.MaxDestinationRules)
				for i := range rows {
					p := destinationTestPolicy(fmt.Sprintf("existing-%d", i))
					p.Priority = i + 1
					p.CreatedAt, p.UpdatedAt = at, at
					rows[i] = destPolicyFromDomain(p)
				}
				if err := r.db.Create(&rows).Error; err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, err := r.ReadDefinitions(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			originalReferences := append([]int64(nil), policy.ListIDs...)
			err = writer.SavePolicyWithList(ctx, &policy, &list, at)
			if err == nil {
				t.Fatalf("%s accepted a failed pair", reason)
			}
			if reason == "quota" {
				var over *destpolicy.DefinitionError
				if !errors.As(err, &over) || over.Detail.Kind != "rules" || over.Detail.Used != protocol.MaxDestinationRules+1 {
					t.Fatalf("quota failed for another reason: %v", err)
				}
			}
			if reason == "policy-name" && !errors.Is(err, domain.ErrAlreadyExists) {
				t.Fatalf("wrong name conflict: %v", err)
			}
			if reason == "generation" && !errors.Is(err, domain.ErrResourceExhausted) || reason == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("wrong %s failure: %v", reason, err)
			}
			after, readErr := r.ReadDefinitions(t.Context())
			if readErr != nil || after.State.Generation != before.State.Generation || len(after.Lists) != len(before.Lists) || len(after.Policies) != len(before.Policies) {
				t.Fatalf("%s left partial definitions: before=%+v after=%+v / %v", reason, before, after, readErr)
			}
			if policy.ID != 0 || len(policy.ListIDs) != len(originalReferences) || len(originalReferences) > 0 && policy.ListIDs[0] != originalReferences[0] || !policy.UpdatedAt.IsZero() || list.ID != 0 || !list.UpdatedAt.IsZero() {
				t.Fatalf("%s mutated caller on rollback", reason)
			}
		})
	}
}
