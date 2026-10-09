package sqlstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

type destinationEntryEditor interface {
	EditListEntries(context.Context, int64, time.Time, func(domain.DestList, bool) (domain.DestList, error)) (domain.DestList, error)
}

func TestDestinationEntryEditReadsUnderLockAndPublishesOnce(t *testing.T) {
	r := newDestDefinitionRepo(t)
	editor, ok := any(r).(destinationEntryEditor)
	if !ok {
		t.Fatal("repository omitted locked custom-entry editor")
	}
	now := time.UnixMilli(1791000000000).UTC()
	p, err := destlist.ParseCustom([]byte("# preserved\ndomain:first.example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	list := domain.DestList{Name: "entries", Kind: domain.DestListCustom, SourceText: []byte("# preserved\ndomain:first.example.test\n"), Entries: p.Entries, EntryCount: p.EntryCount, ContentSHA256: p.ContentSHA256, ParseReport: &p.Report}
	if err := r.SaveList(t.Context(), &list, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	policy := destinationTestPolicy("allow entries")
	policy.Action = domain.DestAllow
	policy.ListIDs = []int64{list.ID}
	if err := r.SavePolicy(t.Context(), &policy, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	edited, err := editor.EditListEntries(t.Context(), list.ID, now, func(current domain.DestList, allow bool) (domain.DestList, error) {
		if !allow || !strings.Contains(string(current.SourceText), "# preserved") {
			t.Fatal("locked edit lost current source or allow usage")
		}
		current.SourceText = append(current.SourceText, []byte("full:second.example.test\n")...)
		parsed, err := destlist.ParseCustom(current.SourceText)
		current.Entries, current.EntryCount, current.ContentSHA256, current.ParseReport = parsed.Entries, parsed.EntryCount, parsed.ContentSHA256, &parsed.Report
		return current, err
	})
	state, _ := r.State(t.Context())
	if err != nil || edited.EntryCount != 2 || !edited.UpdatedAt.After(list.UpdatedAt) || state.Generation != 3 {
		t.Fatal("entry edit did not commit source/report/generation together")
	}
	problem := errors.New("parser failure")
	if _, err := editor.EditListEntries(t.Context(), list.ID, now, func(current domain.DestList, _ bool) (domain.DestList, error) {
		current.SourceText = []byte("bad")
		return current, problem
	}); !errors.Is(err, problem) {
		t.Fatal("failed edit hid parser error")
	}
	after, _ := r.GetList(t.Context(), list.ID)
	state, _ = r.State(t.Context())
	if after.ContentSHA256 != edited.ContentSHA256 || state.Generation != 3 {
		t.Fatal("failed entry edit wrote source or publication")
	}
}

func TestDestinationAllowReferencedListRejectsBroadInputAtomically(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	list := initialModeLists(t)[0]
	if err := r.SaveList(t.Context(), &list, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	policy := destinationTestPolicy("disabled allow")
	policy.Action = domain.DestAllow
	policy.Enabled = false
	policy.ListIDs = []int64{list.ID}
	if err := r.SavePolicy(t.Context(), &policy, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	list.SourceText = []byte("domain:com\nfull:dns.example.test\n")
	parsed, err := destlist.ParseCustom(list.SourceText)
	if err != nil {
		t.Fatal(err)
	}
	list.ParseReport = &parsed.Report
	if err := r.SaveList(t.Context(), &list, list.UpdatedAt, now); !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "dest_list_too_broad") {
		t.Fatal("allow reference accepted ignored broad source")
	}
	state, _ := r.State(t.Context())
	stored, _ := r.GetList(t.Context(), list.ID)
	if state.Generation != 2 || stored.ParseReport.IgnoredBroad != 0 {
		t.Fatal("rejected broad edit partially committed")
	}
}
