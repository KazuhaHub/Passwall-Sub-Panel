package destpolicy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

type groupExceptionFixture struct {
	defs                      domain.DestDefinitions
	groupWrites, globalWrites int
	writeError                error
}

func (s *groupExceptionFixture) ReadDefinitions(context.Context) (domain.DestDefinitions, error) {
	return s.defs, nil
}
func (s *groupExceptionFixture) GetGroupMode(_ context.Context, id int64) (domain.DestGroupMode, error) {
	for _, mode := range s.defs.Groups {
		if mode.GroupID == id {
			return mode, nil
		}
	}
	return domain.DestGroupMode{}, domain.ErrNotFound
}
func (s *groupExceptionFixture) AddGlobalException(context.Context, time.Time, func(domain.DestList) (domain.DestList, error)) (domain.DestGlobalExceptionCommit, error) {
	s.globalWrites++
	return domain.DestGlobalExceptionCommit{}, errors.New("unexpected global write")
}
func (s *groupExceptionFixture) AddGroupException(_ context.Context, id int64, _ time.Time, edit func(domain.DestList) (domain.DestList, error)) (int64, error) {
	s.groupWrites++
	if s.writeError != nil {
		return 0, s.writeError
	}
	for i, list := range s.defs.Lists {
		if list.OwnerGroupID == id {
			// Simulate an append committed after the preflight read. The editor
			// must preserve fresh source from inside the transaction.
			fresh, err := destlist.PrepareEntryPatch(list, true, []string{"full:concurrent.example.test"}, nil)
			if err != nil {
				return 0, err
			}
			patched, err := edit(fresh)
			if err != nil {
				return 0, err
			}
			s.defs.Lists[i] = patched
			return list.ID, nil
		}
	}
	return 0, domain.ErrNotFound
}
func newGroupExceptionFixture(t *testing.T) *groupExceptionFixture {
	t.Helper()
	parsed, err := destlist.ParseCustom([]byte("# keep comment\nfull:existing.example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	return &groupExceptionFixture{defs: domain.DestDefinitions{
		Groups: []domain.DestGroupMode{{GroupID: 7, Mode: "allowlist", Stage: "trial", ExtraListID: 8}},
		Lists:  []domain.DestList{{ID: 8, Name: "Private extra", OwnerGroupID: 7, Kind: domain.DestListCustom, SourceText: []byte("# keep comment\nfull:existing.example.test\n"), Entries: parsed.Entries, EntryCount: parsed.EntryCount, ContentSHA256: parsed.ContentSHA256, ParseReport: &parsed.Report}},
	}}
}
func TestGroupExceptionNormalizesLocallyAndPatchesFreshOwnedSourceOnly(t *testing.T) {
	s := newGroupExceptionFixture(t)
	result, err := NewExceptionManager(s).Group(t.Context(), 7, "https://Login.Example.co.uk/path?private=value", "site")
	if err != nil || result.Entry != "domain:example.co.uk" || result.Commit.ListID != 8 || result.Commit.PolicyID != 0 || result.Commit.Created || s.groupWrites != 1 || s.globalWrites != 0 {
		t.Fatalf("group append result=%+v: %v", result, err)
	}
	text := string(s.defs.Lists[0].SourceText)
	for _, retained := range []string{"# keep comment", "full:existing.example.test", "full:concurrent.example.test", "domain:example.co.uk"} {
		if !strings.Contains(text, retained) {
			t.Fatal("group exception overwrote current private source")
		}
	}
	if strings.Contains(text, "private=value") {
		t.Fatal("URL path/query entered list")
	}
}
func TestGroupExceptionRejectsClosedMissingForeignAndUnsafeTargetsWithoutWrites(t *testing.T) {
	for _, name := range []string{"closed", "missing", "foreign", "not-extra", "broad", "invalid-id"} {
		t.Run(name, func(t *testing.T) {
			s := newGroupExceptionFixture(t)
			id, target := int64(7), "example.test"
			switch name {
			case "closed":
				s.defs.Groups[0].Mode, s.defs.Groups[0].Stage = "open", ""
			case "missing":
				s.defs.Groups = nil
			case "foreign":
				s.defs.Lists[0].OwnerGroupID = 9
			case "not-extra":
				s.defs.Groups[0].ExtraListID = 99
			case "broad":
				target = "com"
			case "invalid-id":
				id = 0
			}
			if result, err := NewExceptionManager(s).Group(t.Context(), id, target, "site"); err == nil || result != (ExceptionResult{}) || s.groupWrites != 0 || s.globalWrites != 0 {
				t.Fatal("invalid group exception reached a write or returned a partial identity")
			}
		})
	}
	s := newGroupExceptionFixture(t)
	s.writeError = errors.New("transaction failure")
	if result, err := NewExceptionManager(s).Group(t.Context(), 7, "example.test", "host"); !errors.Is(err, s.writeError) || result != (ExceptionResult{}) {
		t.Fatal("failed transaction returned a partial identity")
	}
}
