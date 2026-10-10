package destpolicy

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func groupTrialReporterFixture(t *testing.T) (*GroupTrialReporter, *domain.DestDefinitions, *domain.DestGroupTrialQuery, *int) {
	t.Helper()
	base, extra, selected, pending := readyList(1, "domain:owned.test\n"), readyList(2, "domain:covered.test\nfull:exact.test\nregexp:^regex\\.test$\n"), readyList(3, "domain:parent.test\n"), readyList(4, "domain:pending.test\n")
	base.OwnerGroupID, extra.OwnerGroupID = 7, 7
	pending.Kind, pending.LastFetchedAt = domain.DestListRemote, nil
	defs := domain.DestDefinitions{Lists: []domain.DestList{base, extra, selected, pending}, Groups: []domain.DestGroupMode{{GroupID: 7, Mode: "allowlist", Stage: "trial", BaseListID: 1, ExtraListID: 2, ListIDs: []int64{3, 4}}}}
	now := time.UnixMilli(1_800_000_000_000).UTC()
	var query domain.DestGroupTrialQuery
	calls := 0
	reporter := NewGroupTrialReporter(
		func(context.Context, int64) (*domain.Group, error) { return &domain.Group{ID: 7}, nil },
		func(context.Context) (domain.DestDefinitions, error) { return defs, nil },
		func(_ context.Context, q domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error) {
			query, calls = q, calls+1
			data := domain.DestGroupTrialData{Items: []domain.DestGroupTrialSite{}, DroppedInRange: 2, Losses: domain.DestAuditLosses{Rows: 2, Events: 3, Unmatched: 4, Scope: "panel"}}
			for _, site := range []string{"owned.test", "covered.test", "sub.parent.test", "exact.test", "regex.test", "pending.test", "(ip)"} {
				data.Items = append(data.Items, domain.DestGroupTrialSite{Site: site, Count: 1, LastAt: now.UnixMilli()})
			}
			return data, nil
		},
		func(context.Context) (int, error) { return 2, nil })
	reporter.now = func() time.Time { return now }
	return reporter, &defs, &query, &calls
}

func TestGroupTrialReportClampsRetentionAndMarksOnlyWholeSiteCoverage(t *testing.T) {
	r, defs, query, calls := groupTrialReporterFixture(t)
	before := append([]byte{}, defs.Lists[1].Entries...)
	got, err := r.Read(t.Context(), 7, 31)
	if err != nil || got.EffectiveDays != 2 || len(got.Items) != 7 || *calls != 1 || query.GroupID != 7 || query.Until.Sub(query.Since) != 48*time.Hour {
		t.Fatalf("bounded report: %+v %v query=%+v", got, err, query)
	}
	for i, item := range got.Items {
		if item.Added != (i < 3) {
			t.Errorf("site %s coverage=%t", item.Site, item.Added)
		}
	}
	if got.Losses != (domain.DestAuditLosses{Rows: 2, Events: 3, Unmatched: 4, Scope: "panel"}) || got.DroppedInRange != 2 || !reflect.DeepEqual(before, defs.Lists[1].Entries) {
		t.Fatal("report changed independent losses or current definitions")
	}
	defs.Lists[1] = readyList(2, "domain:exact.test\n")
	defs.Lists[1].OwnerGroupID = 7
	got, err = r.Read(t.Context(), 7, 1)
	if err != nil || got.EffectiveDays != 1 || got.Items[1].Added || !got.Items[3].Added {
		t.Fatal("report cached an obsolete current-list coverage decision")
	}
	now := r.now()
	defs.Lists[3].LastFetchedAt, defs.Lists[3].LastError = &now, "refresh failed after a successful fetch"
	got, err = r.Read(t.Context(), 7, 1)
	if err != nil || !got.Items[5].Added {
		t.Fatal("refresh failure discarded last-good fetched coverage")
	}
}

func TestGroupTrialReportFailsClosedForMissingGroupCorruptOwnershipAndReadErrors(t *testing.T) {
	for _, kind := range []string{"missing", "foreign", "definitions", "data", "retention"} {
		t.Run(kind, func(t *testing.T) {
			r, defs, _, calls := groupTrialReporterFixture(t)
			switch kind {
			case "missing":
				r.readGroup = func(context.Context, int64) (*domain.Group, error) { return nil, domain.ErrNotFound }
			case "foreign":
				defs.Lists[1].OwnerGroupID = 8
			case "definitions":
				r.readDefinitions = func(context.Context) (domain.DestDefinitions, error) {
					return domain.DestDefinitions{}, errors.New("private-definition-detail")
				}
			case "data":
				r.readData = func(context.Context, domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error) {
					return domain.DestGroupTrialData{Items: []domain.DestGroupTrialSite{{Site: "private-partial.test", Count: 1}}}, errors.New("private-storage-detail")
				}
			case "retention":
				r.retention = func(context.Context) (int, error) { return 0, nil }
			}
			got, err := r.Read(t.Context(), 7, 7)
			if err == nil || !reflect.DeepEqual(got, GroupTrialReport{}) || *calls != 0 {
				t.Fatal("failed report returned partial data or queried before validation")
			}
		})
	}
}

func TestGroupTrialReportRejectsUnsafeInputsAndAllowsRetainedClosedGroupHistory(t *testing.T) {
	r, defs, _, calls := groupTrialReporterFixture(t)
	for _, input := range [][2]int{{0, 7}, {-1, 7}, {7, 0}, {7, -1}} {
		if _, err := r.Read(t.Context(), int64(input[0]), input[1]); !errors.Is(err, domain.ErrValidation) {
			t.Fatal("unsafe group report input accepted")
		}
	}
	if *calls != 0 {
		t.Fatal("invalid report read storage")
	}
	defs.Groups[0].Mode, defs.Groups[0].Stage = "open", ""
	got, err := r.Read(t.Context(), 7, 7)
	if err != nil || len(got.Items) != 7 || !got.Items[0].Added {
		t.Fatal("closing a group erased retained trial history or lists")
	}
	var missing *GroupTrialReporter
	if _, err := missing.Read(t.Context(), 7, 7); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("unwired report reader did not fail closed")
	}
}
