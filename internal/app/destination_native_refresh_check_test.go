package app

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

//go:embed testdata/native-list-refresh.txt
var nativeListRefreshFixture []byte

func nativeRefreshSourceURL(commit string) string {
	if len(commit) != 40 || strings.IndexFunc(commit, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) != -1 {
		return ""
	}
	return "https://raw.githubusercontent.com/KazuhaHub/Passwall-Sub-Panel/" + commit + "/internal/app/testdata/native-list-refresh.txt"
}

func nativeReportRefreshMatches(prior, current domain.DestList, before, now domain.DestPolicyState, after time.Time, expected domain.DestParseReport) bool {
	want, wantErr := json.Marshal(expected)
	got, gotErr := json.Marshal(current.ParseReport)
	return wantErr == nil && gotErr == nil && prior.ParseReport == nil && current.ParseReport != nil && bytes.Equal(want, got) &&
		prior.ID > 0 && current.ID == prior.ID && prior.Kind == domain.DestListRemote && current.Kind == prior.Kind && current.SourceURL == prior.SourceURL &&
		prior.ContentSHA256 != "" && current.ContentSHA256 == prior.ContentSHA256 && len(prior.Entries) > 0 && bytes.Equal(prior.Entries, current.Entries) &&
		current.EntryCount == prior.EntryCount && current.RegexpCount == prior.RegexpCount && current.LastError == "" &&
		prior.LastFetchedAt != nil && current.LastFetchedAt != nil && current.LastFetchedAt.After(after) && current.UpdatedAt.After(prior.UpdatedAt) &&
		before.Generation > 0 && before.Generation == before.PublishedGeneration && now.Generation == before.Generation && now.PublishedGeneration == before.PublishedGeneration && now.Paused == before.Paused
}

func TestNativeRefreshSourceRequiresImmutableOwnedCommit(t *testing.T) {
	commit := strings.Repeat("a", 40)
	if got := nativeRefreshSourceURL(commit); got != "https://raw.githubusercontent.com/KazuhaHub/Passwall-Sub-Panel/"+commit+"/internal/app/testdata/native-list-refresh.txt" {
		t.Fatal("refresh fixture source is not the immutable owned public file")
	}
	for _, invalid := range []string{"", "main", strings.Repeat("a", 39), strings.Repeat("g", 40), commit + "\n", "https://127.0.0.1/", strings.Repeat("A", 40)} {
		if nativeRefreshSourceURL(invalid) != "" {
			t.Fatal("refresh fixture admitted an unreviewed source")
		}
	}
}

func TestNativeReportRefreshRequiresNewReportAndUnchangedContent(t *testing.T) {
	parsed, err := destlist.ParseRemote(nativeListRefreshFixture)
	if err != nil || parsed.EntryCount != 1 || parsed.Report.Accepted != 1 || parsed.Report.Ignored != 1 {
		t.Fatal("remote fixture does not exercise duplicate report metadata")
	}
	old, after, fetched := time.Unix(100, 0), time.Unix(101, 0), time.Unix(102, 0)
	prior := domain.DestList{ID: 1, Kind: domain.DestListRemote, SourceURL: nativeRefreshSourceURL(strings.Repeat("a", 40)), Entries: parsed.Entries, ContentSHA256: parsed.ContentSHA256, EntryCount: 1, LastFetchedAt: &old, UpdatedAt: old}
	state := domain.DestPolicyState{Generation: 7, PublishedGeneration: 7}
	for _, tc := range []struct {
		name   string
		change func(*domain.DestList, *domain.DestPolicyState)
		want   bool
	}{
		{"new-report-same-list", func(*domain.DestList, *domain.DestPolicyState) {}, true},
		{"report-still-missing", func(l *domain.DestList, _ *domain.DestPolicyState) { l.ParseReport = nil }, false},
		{"wrong-report", func(l *domain.DestList, _ *domain.DestPolicyState) {
			l.ParseReport = &domain.DestParseReport{Accepted: 1}
		}, false},
		{"old-fetch", func(l *domain.DestList, _ *domain.DestPolicyState) { l.LastFetchedAt = &old }, false},
		{"missing-fetch", func(l *domain.DestList, _ *domain.DestPolicyState) { l.LastFetchedAt = nil }, false},
		{"old-edit-version", func(l *domain.DestList, _ *domain.DestPolicyState) { l.UpdatedAt = old }, false},
		{"failed-fetch", func(l *domain.DestList, _ *domain.DestPolicyState) { l.LastError = "failed" }, false},
		{"other-list", func(l *domain.DestList, _ *domain.DestPolicyState) { l.ID++ }, false},
		{"source-changed", func(l *domain.DestList, _ *domain.DestPolicyState) { l.SourceURL += "other" }, false},
		{"body-changed", func(l *domain.DestList, _ *domain.DestPolicyState) { l.Entries = []byte("other") }, false},
		{"hash-changed", func(l *domain.DestList, _ *domain.DestPolicyState) { l.ContentSHA256 = "other" }, false},
		{"definition-advanced", func(_ *domain.DestList, s *domain.DestPolicyState) { s.Generation++ }, false},
		{"publication-advanced", func(_ *domain.DestList, s *domain.DestPolicyState) { s.PublishedGeneration++ }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, nowState := prior, state
			current.ParseReport, current.LastFetchedAt, current.UpdatedAt = &parsed.Report, &fetched, fetched
			tc.change(&current, &nowState)
			if got := nativeReportRefreshMatches(prior, current, state, nowState, after, parsed.Report); got != tc.want {
				t.Fatalf("report-only refresh = %t, want %t", got, tc.want)
			}
		})
	}
}
