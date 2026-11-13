package destlist

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The admin's edits to a list that are not a whole-list rewrite: adding and
// removing entries in one step (the "add to the exceptions" and "add to the
// allowlist" buttons call this, so the page never rebuilds the whole text),
// previewing a remote list, and saying which policies use a list.

func TestAddEntries_AppendsAndRemovesInOneWrite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	l, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("# keep this comment\nDOMAIN-SUFFIX,a.example.com\nb.example.com\n"))
	g := h.generation()

	got, report, err := h.svc.AddEntries(ctx, l.ID, []string{"DOMAIN-SUFFIX,c.example.com", "d.example.com"}, []string{"b.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.Accepted != 3 {
		t.Fatalf("report = %+v", report)
	}
	stored, _ := h.repos.Lists.GetByID(ctx, l.ID)
	if string(stored.Entries) != "domain:a.example.com\ndomain:c.example.com\ndomain:d.example.com" {
		t.Fatalf("entries = %q", stored.Entries)
	}
	if !strings.Contains(string(stored.SourceText), "# keep this comment") || strings.Contains(string(stored.SourceText), "b.example.com") {
		t.Fatalf("text = %q: comments stay, the removed line goes", stored.SourceText)
	}
	if got.EntryCount != 3 || h.generation() != g+1 {
		t.Fatalf("count %d, generation %d -> %d: one change is one advance", got.EntryCount, g, h.generation())
	}
}

func TestAddEntries_RemovalMatchesTheCanonicalEntryNotTheSpelling(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	l, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("DOMAIN-SUFFIX,Example.COM\nother.example.net"))
	if _, _, err := h.svc.AddEntries(ctx, l.ID, nil, []string{"example.com"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := h.repos.Lists.GetByID(ctx, l.ID)
	if string(stored.Entries) != "domain:other.example.net" {
		t.Fatalf("entries = %q", stored.Entries)
	}
}

func TestAddEntries_RefusesWhatIsNotACustomListAndWhatDoesNotParse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	custom, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("example.com"))
	if _, _, err := h.svc.AddEntries(ctx, custom.ID, []string{"regexp:(oops"}, nil); err == nil {
		t.Fatal("an entry that does not parse was added")
	}
	if _, _, err := h.svc.AddEntries(ctx, custom.ID, nil, nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nothing to do: %v", err)
	}
	remote := &domain.DestList{Name: "r", Kind: domain.DestListRemote, Side: domain.DestSideDest, SourceURL: "https://example.com/x"}
	if err := h.repos.Lists.Create(ctx, remote); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.AddEntries(ctx, remote.ID, []string{"a.example.com"}, nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a remote list has no text to edit: %v", err)
	}
	if _, _, err := h.svc.AddEntries(ctx, 9999, []string{"a.example.com"}, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing list: %v", err)
	}
	stored, _ := h.repos.Lists.GetByID(ctx, custom.ID)
	if string(stored.Entries) != "domain:example.com" {
		t.Fatalf("a refused edit changed the list: %q", stored.Entries)
	}
}

func TestAddEntries_ConcurrentCallsDoNotLoseAnEdit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	l, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("base.example.com"))
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, _, err := h.svc.AddEntries(ctx, l.ID, []string{string(rune('a'+i)) + ".example.com"}, nil)
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	stored, _ := h.repos.Lists.GetByID(ctx, l.ID)
	if stored.EntryCount != 9 {
		t.Fatalf("entries = %d, want 9: an edit was lost", stored.EntryCount)
	}
}

func TestPreviewRemote_ReportsWhatWasFetchedWithoutStoringIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("a.example.com\nb.example.com\n")) })
	h.svc.doer = srv.Client()

	p, err := h.svc.PreviewRemote(ctx, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Parsed.EntryCount != 2 || p.HTTPStatus != 200 || p.Bytes != len("a.example.com\nb.example.com\n") {
		t.Fatalf("preview = %+v", p)
	}
	if all, _ := h.svc.Lists(ctx); len(all) != 0 {
		t.Fatal("a preview stored a list")
	}
	if _, err := h.svc.PreviewRemote(ctx, "http://insecure.example.com/x", ""); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("http url: %v", err)
	}
}

func TestUpdateRemote_RenamesAndRefetchesOnAUrlChange(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	body := "a.example.com\n"
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
	h.svc.doer = srv.Client()
	l, _, err := h.svc.CreateRemote(ctx, "feed", srv.URL+"/one", "")
	if err != nil {
		t.Fatal(err)
	}
	g := h.generation()
	// A rename alone fetches nothing and moves nothing.
	if _, _, err := h.svc.UpdateRemote(ctx, l.ID, "feed2", l.SourceURL); err != nil {
		t.Fatal(err)
	}
	if h.generation() != g {
		t.Fatal("a rename moved the generation")
	}
	body = "z.example.com\n"
	got, _, err := h.svc.UpdateRemote(ctx, l.ID, "feed2", srv.URL+"/two")
	if err != nil || got.SourceURL != srv.URL+"/two" || string(got.Entries) != "domain:z.example.com" {
		t.Fatalf("after the url change: (%+v, %v)", got, err)
	}
	if _, _, err := h.svc.UpdateRemote(ctx, l.ID, "feed2", "http://insecure.example.com"); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("insecure: %v", err)
	}
}

func TestUsedBy_NamesThePoliciesThatReferenceEachList(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, _, _ := h.svc.CreateCustom(ctx, "a", "", []byte("a.example.com"))
	b, _, _ := h.svc.CreateCustom(ctx, "b", "", []byte("b.example.com"))
	p1 := h.policy("p1", domain.DestStateOn, domain.DestActionBlock, a.ID)
	p2 := h.policy("p2", domain.DestStateOff, domain.DestActionBlock, a.ID, b.ID)
	used, err := h.svc.UsedBy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(used[a.ID]) != 2 || used[a.ID][0].ID != p1.ID || used[a.ID][1].ID != p2.ID || used[a.ID][0].Kind != "policy" || used[a.ID][1].Name != "p2" {
		t.Fatalf("used[a] = %+v", used[a.ID])
	}
	if len(used[b.ID]) != 1 {
		t.Fatalf("used[b] = %+v", used[b.ID])
	}
}

func TestRefreshMarker(t *testing.T) {
	h := newHarness(t)
	if !h.svc.BeginRefresh(5) || h.svc.BeginRefresh(5) {
		t.Fatal("a second refresh of the same list must not start")
	}
	if !h.svc.Refreshing(5) || h.svc.Refreshing(6) {
		t.Fatal("marker")
	}
	h.svc.EndRefresh(5)
	if h.svc.Refreshing(5) || !h.svc.BeginRefresh(5) {
		t.Fatal("marker not cleared")
	}
}
