//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

func nativeSeedReportRefreshList(t *testing.T, a *App) domain.DestList {
	t.Helper()
	url := nativeRefreshSourceURL(os.Getenv("PSP_NATIVE_SOURCE_COMMIT"))
	if url == "" {
		t.Fatal("report refresh requires the immutable owned source fixture")
	}
	parsed, err := destlist.ParseRemote(nativeListRefreshFixture)
	nativeMust(t, err, "parse owned remote fixture")
	now := time.Now().UTC()
	oldFetch := now.Add(-time.Hour)
	// A migrated legacy row may have usable content but no parser report. The
	// following refresh must download and parse normally through production HTTP.
	list := domain.DestList{Name: "Native report-only refresh", Kind: domain.DestListRemote, SourceURL: url, Entries: parsed.Entries, ContentSHA256: parsed.ContentSHA256, EntryCount: parsed.EntryCount, RegexpCount: parsed.RegexpCount, LastFetchedAt: &oldFetch}
	nativeMust(t, a.destDefinitions.SaveList(t.Context(), &list, time.Time{}, now), "seed legacy remote list")
	return list
}

func nativeRefreshReportOnly(t *testing.T, a *App, list domain.DestList, request func(string, string, any, int, any)) {
	t.Helper()
	before, err := a.destDefinitions.State(t.Context())
	nativeMust(t, err, "read report refresh generation")
	parsed, err := destlist.ParseRemote(nativeListRefreshFixture)
	nativeMust(t, err, "parse expected remote report")
	after := time.Now().UTC()
	request("POST", fmt.Sprintf("dest/lists/%d/refresh", list.ID), nil, 202, nil)
	nativeWait(t, 45*time.Second, "report-only remote refresh", func() bool {
		current, err := a.destDefinitions.GetList(t.Context(), list.ID)
		if err != nil || a.destLists.IsRefreshing(list.ID) {
			return false
		}
		if current.LastError != "" {
			t.Fatal("report-only remote refresh failed; source diagnostics withheld")
		}
		state, err := a.destDefinitions.State(t.Context())
		return err == nil && nativeReportRefreshMatches(list, current, before, state, after, parsed.Report)
	})
	var detail struct {
		Report        *domain.DestParseReport `json:"parse_report"`
		LastFetchedAt int64                   `json:"last_fetched_at"`
		LastError     string                  `json:"last_error"`
		State         string                  `json:"state"`
	}
	request("GET", fmt.Sprintf("dest/lists/%d", list.ID), nil, 200, &detail)
	want, wantErr := json.Marshal(parsed.Report)
	got, gotErr := json.Marshal(detail.Report)
	if wantErr != nil || gotErr != nil || !bytes.Equal(want, got) || detail.LastFetchedAt <= after.UnixMilli() || detail.LastError != "" || detail.State != "ready" {
		t.Fatal("report-only refresh was not readable through administrator HTTP")
	}
}
