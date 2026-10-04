package sqlstore

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestDestinationListReportSurvivesSaveReadAndMetadataOnlyRefresh(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	list := domain.DestList{Name: "report", Kind: domain.DestListGeosite, GeositeCategory: "finance", Entries: []byte("domain:example.com\n"), EntryCount: 1, ContentSHA256: "same", ParseReport: &domain.DestParseReport{Accepted: 1, Ignored: 1, IgnoredBroad: 1, Samples: []domain.DestParseSample{{Line: 2, Text: "domain:com", Reason: "broad_entry"}}}}
	if err := r.SaveList(t.Context(), &list, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	if list.ParseReport == nil || list.ParseReport.IgnoredBroad != 1 {
		t.Fatalf("save lost report: %+v", list)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || defs.Lists[0].ParseReport == nil || defs.Lists[0].ParseReport.Samples[0].Line != 2 {
		t.Fatalf("read lost report: %+v / %v", defs, err)
	}
	// Mutating either DTO must not mutate the other or the persisted report.
	list.ParseReport.Samples[0].Text = "mutated"
	if defs.Lists[0].ParseReport.Samples[0].Text != "domain:com" {
		t.Fatal("report aliases caller DTO")
	}
	captured := defs.Lists[0]
	next := domain.DestParseReport{Accepted: 1, Ignored: 2, IgnoredBroad: 2, Samples: []domain.DestParseSample{{Line: 4, Text: "domain:co.uk", Reason: "broad_entry"}}}
	if err := r.CommitListRefresh(t.Context(), captured, domain.DestListRefresh{ContentSHA256: "same", EntryCount: 1, ParseReport: &next}, now); err != nil {
		t.Fatal(err)
	}
	defs, err = r.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 1 || defs.Lists[0].ParseReport.IgnoredBroad != 2 || !defs.Lists[0].UpdatedAt.After(captured.UpdatedAt) {
		t.Fatalf("metadata refresh incorrectly published/lost report: %+v / %v", defs, err)
	}
	if err := r.CommitListRefresh(t.Context(), captured, domain.DestListRefresh{ContentSHA256: "same", ParseReport: &domain.DestParseReport{}}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale report accepted: %v", err)
	}
	current := defs.Lists[0]
	if err := r.CommitListRefresh(t.Context(), current, domain.DestListRefresh{LastError: "dest_list_empty_after_filter", ParseReport: &domain.DestParseReport{}}, now); err != nil {
		t.Fatal(err)
	}
	defs, err = r.ReadDefinitions(t.Context())
	if err != nil || defs.Lists[0].ParseReport.IgnoredBroad != 2 || defs.Lists[0].ContentSHA256 != "same" || string(defs.Lists[0].Entries) != "domain:example.com\n" || defs.State.Generation != 1 {
		t.Fatalf("failure overwrote successful content/report: %+v / %v", defs, err)
	}
}

func TestDestinationLegacyListReportRemainsAbsentUntilSuccessfulParse(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	list := domain.DestList{Name: "legacy", Kind: domain.DestListCustom}
	if err := r.SaveList(t.Context(), &list, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || defs.Lists[0].ParseReport != nil {
		t.Fatalf("invented legacy report: %+v / %v", defs, err)
	}
	version := list.UpdatedAt
	list.ParseReport = &domain.DestParseReport{Ignored: 1, Samples: []domain.DestParseSample{{Line: 1, Text: "#comment", Reason: "comment"}}}
	if err := r.SaveList(t.Context(), &list, version, now); err != nil {
		t.Fatal(err)
	}
	defs, err = r.ReadDefinitions(t.Context())
	if err != nil || defs.Lists[0].ParseReport == nil || defs.State.Generation != 1 || !list.UpdatedAt.After(version) {
		t.Fatalf("report-only edit lost/published: %+v / %v", defs, err)
	}
}
