package destcatalog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

var locales = []string{"zh-CN", "en-US"}

// The embedded catalog is data that ships with the binary, so its shape is
// tested like code: a typo here is a template that fails for every admin.
func TestEmbeddedCatalog_IsWellFormed(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Version < 1 {
		t.Errorf("catalog_version = %d", c.Version)
	}
	portsRE := regexp.MustCompile(`^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$`)
	seen := map[string]bool{}
	for _, tpl := range c.Templates {
		if tpl.Key == "" || len(tpl.Key) > domain.DestTemplateKeyMax || !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(tpl.Key) {
			t.Errorf("template key %q", tpl.Key)
		}
		if seen[tpl.Key] {
			t.Errorf("template key %q is listed twice", tpl.Key)
		}
		seen[tpl.Key] = true
		for _, loc := range locales {
			if name := tpl.Name[loc]; name == "" || !domain.ValidDestName(name) {
				t.Errorf("%s: name[%s] = %q", tpl.Key, loc, name)
			}
			if d := tpl.Description[loc]; d == "" || len(d) > domain.DestDescriptionMax {
				t.Errorf("%s: description[%s] is %d bytes (1..%d)", tpl.Key, loc, len(d), domain.DestDescriptionMax)
			}
		}
		if len(tpl.Geosite) == 0 && len(tpl.Protocols) == 0 && tpl.Ports == "" && !tpl.Private {
			t.Errorf("%s matches nothing", tpl.Key)
		}
		for _, cat := range tpl.Geosite {
			if cat != strings.ToLower(cat) || strings.ContainsAny(cat, " ,@:") {
				t.Errorf("%s: geosite category %q is not a bare lower-case name", tpl.Key, cat)
			}
		}
		if len(slices.Compact(slices.Sorted(slices.Values(tpl.Geosite)))) != len(tpl.Geosite) {
			t.Errorf("%s: a geosite category is listed twice", tpl.Key)
		}
		for _, p := range tpl.Protocols {
			if p != "bittorrent" {
				t.Errorf("%s: protocol %q (the wire knows only bittorrent)", tpl.Key, p)
			}
		}
		if tpl.Ports != "" && !portsRE.MatchString(tpl.Ports) {
			t.Errorf("%s: ports %q", tpl.Key, tpl.Ports)
		}
		if tpl.Network != "" && tpl.Network != "tcp" && tpl.Network != "udp" {
			t.Errorf("%s: network %q", tpl.Key, tpl.Network)
		}
	}
	// The thirteen templates of the plan, by key.
	for _, key := range []string{"bittorrent", "pt", "finance", "crypto", "ecommerce", "scholar", "porn", "ads", "proxy", "doh", "speedtest", "smtp", "private"} {
		if !seen[key] {
			t.Errorf("template %q is missing", key)
		}
	}
}

// The categories v2fly does not have are listed, honestly, and create nothing.
func TestEmbeddedCatalog_ListsTheMissingCategoriesAsUnavailable(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"education", "gambling", "payment"}
	var got []string
	for _, u := range c.Unavailable {
		got = append(got, u.Key)
		if seenTemplate := func() bool { _, ok := c.Template(u.Key); return ok }(); seenTemplate {
			t.Errorf("%q is both a template and unavailable", u.Key)
		}
		for _, loc := range locales {
			if u.Name[loc] == "" || len(u.Reason[loc]) < 20 {
				t.Errorf("%s: name/reason[%s] = %q / %q", u.Key, loc, u.Name[loc], u.Reason[loc])
			}
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("unavailable = %v, want %v", got, want)
	}
}

func TestLoad_RejectsABrokenCatalog(t *testing.T) {
	for name, doc := range map[string]string{
		"not json":       `{`,
		"no version":     `{"templates":[]}`,
		"duplicate key":  `{"catalog_version":1,"templates":[{"key":"a","name":{"zh-CN":"a","en-US":"a"},"description":{"zh-CN":"a","en-US":"a"},"private":true},{"key":"a","name":{"zh-CN":"a","en-US":"a"},"description":{"zh-CN":"a","en-US":"a"},"private":true}]}`,
		"empty template": `{"catalog_version":1,"templates":[{"key":"a","name":{"zh-CN":"a","en-US":"a"},"description":{"zh-CN":"a","en-US":"a"}}]}`,
		"missing locale": `{"catalog_version":1,"templates":[{"key":"a","name":{"zh-CN":"a"},"description":{"zh-CN":"a","en-US":"a"},"private":true}]}`,
		"bad protocol":   `{"catalog_version":1,"templates":[{"key":"a","name":{"zh-CN":"a","en-US":"a"},"description":{"zh-CN":"a","en-US":"a"},"protocols":["quic"]}]}`,
		"bad ports":      `{"catalog_version":1,"templates":[{"key":"a","name":{"zh-CN":"a","en-US":"a"},"description":{"zh-CN":"a","en-US":"a"},"ports":"80,"}]}`,
		"unknown field":  `{"catalog_version":1,"templates":[],"surprise":1}`,
	} {
		if _, err := parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// ---- instantiate ----

type fakeLists struct {
	events  *[]string
	ensured int
	specs   map[string]domain.DestList
	err     map[string]error
}

func (f *fakeLists) EnsureGeositeData(context.Context) error {
	f.ensured++
	f.log("ensure")
	return nil
}
func (f *fakeLists) LockGeosite(context.Context) (func(), error) {
	f.log("lock")
	return func() { f.log("unlock") }, nil
}
func (f *fakeLists) log(e string) {
	if f.events != nil {
		*f.events = append(*f.events, e)
	}
}
func (f *fakeLists) GeositeListSpec(_ context.Context, cat string, attrs []string) (domain.DestList, error) {
	f.log("spec")
	if err := f.err[cat]; err != nil {
		return domain.DestList{}, err
	}
	if len(attrs) != 0 {
		return domain.DestList{}, fmt.Errorf("catalog lists take no attrs")
	}
	return f.specs[cat], nil
}

type fakeStore struct {
	events *[]string
	lists  []domain.DestList
	pol    *domain.DestPolicy
	err    error
	added  []*domain.DestPolicy
}

func (f *fakeStore) InstantiateTemplate(_ context.Context, lists []domain.DestList, p *domain.DestPolicy) (*domain.DestPolicy, error) {
	if f.events != nil {
		*f.events = append(*f.events, "store")
	}
	if f.err != nil {
		return nil, f.err
	}
	f.lists, f.pol = lists, p
	p.ID = 7
	return p, nil
}
func (f *fakeStore) List(context.Context) ([]*domain.DestPolicy, error) { return f.added, nil }

func newService(t *testing.T) (*Service, *fakeLists, *fakeStore) {
	t.Helper()
	fl := &fakeLists{specs: map[string]domain.DestList{}, err: map[string]error{}}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range c.Templates {
		for _, cat := range tpl.Geosite {
			fl.specs[cat] = domain.DestList{Name: cat, Kind: domain.DestListGeosite, GeositeCategory: cat, EntryCount: 1}
		}
	}
	fs := &fakeStore{}
	svc, err := New(fl, fs, fs)
	if err != nil {
		t.Fatal(err)
	}
	return svc, fl, fs
}

func TestInstantiate_BuildsTheListsAndThePolicyOfATemplate(t *testing.T) {
	svc, lists, store := newService(t)
	got, err := svc.Instantiate(context.Background(), "bittorrent", "en-US", domain.DestStateOn)
	if err != nil {
		t.Fatal(err)
	}
	if lists.ensured != 1 {
		t.Fatalf("geosite data ensured %d times, want once", lists.ensured)
	}
	p := store.pol
	if got != p || p.Name != "BitTorrent" || p.Action != domain.DestActionBlock || p.State != domain.DestStateOn ||
		p.TemplateKey != "bittorrent" || p.Scope != domain.DestScopeAll || !slices.Equal(p.Inline.Protocols, []string{"bittorrent"}) ||
		p.Description == "" || len(store.lists) != 1 || store.lists[0].GeositeCategory != "category-public-tracker" {
		t.Fatalf("policy = %+v lists = %+v", p, store.lists)
	}

	// The name and description follow the locale; zh-CN is the default.
	if _, err := svc.Instantiate(context.Background(), "pt", "", domain.DestStateOff); err != nil || store.pol.Name != "PT 站" {
		t.Fatalf("default locale: %+v %v", store.pol, err)
	}
	// A template with several categories gets one list each, in order.
	if _, err := svc.Instantiate(context.Background(), "finance", "en-US", domain.DestStateOff); err != nil || len(store.lists) != 7 || store.lists[0].GeositeCategory != "category-finance" {
		t.Fatalf("finance lists = %d, %v", len(store.lists), err)
	}
	// Ports and the private flag go to the policy's own match; no lists, and no
	// geosite data is needed for them.
	before := lists.ensured
	if _, err := svc.Instantiate(context.Background(), "smtp", "en-US", domain.DestStateOn); err != nil {
		t.Fatal(err)
	}
	if store.pol.Inline.Ports != "25,465,587" || store.pol.Inline.Network != "tcp" || len(store.lists) != 0 || lists.ensured != before {
		t.Fatalf("smtp = %+v lists %d ensured %d", store.pol.Inline, len(store.lists), lists.ensured)
	}
	if _, err := svc.Instantiate(context.Background(), "private", "en-US", domain.DestStateOff); err != nil || !store.pol.Inline.Private {
		t.Fatalf("private = %+v %v", store.pol, err)
	}
}

func TestInstantiate_TheAdminChoosesTheState(t *testing.T) {
	svc, _, store := newService(t)
	for _, st := range []domain.DestState{domain.DestStateOff, domain.DestStateReport, domain.DestStateOn} {
		if _, err := svc.Instantiate(context.Background(), "pt", "en-US", st); err != nil || store.pol.State != st {
			t.Errorf("state %s: %+v %v", st, store.pol, err)
		}
	}
	// There is no default: an empty state is refused, as is one that does not
	// exist.
	for _, st := range []domain.DestState{"", "maybe"} {
		if _, err := svc.Instantiate(context.Background(), "pt", "en-US", st); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("state %q: %v, want a validation error", st, err)
		}
	}
}

func TestInstantiate_Refusals(t *testing.T) {
	svc, lists, store := newService(t)
	ctx := context.Background()
	if _, err := svc.Instantiate(ctx, "nope", "en-US", domain.DestStateOn); !errors.Is(err, ErrUnknownTemplate) || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown template: %v", err)
	}
	for _, key := range []string{"education", "payment", "gambling"} {
		if _, err := svc.Instantiate(ctx, key, "en-US", domain.DestStateOn); !errors.Is(err, ErrTemplateUnavailable) || !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: %v, want ErrTemplateUnavailable", key, err)
		}
	}
	if store.pol != nil {
		t.Fatal("an unavailable template created a policy")
	}
	// A category the data lacks fails the whole instantiation, naming it.
	lists.err["category-pt"] = fmt.Errorf("%w: geosite category %q is not in the current data", domain.ErrUnavailable, "category-pt")
	if _, err := svc.Instantiate(ctx, "pt", "en-US", domain.DestStateOn); !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "category-pt") {
		t.Fatalf("a missing category: %v", err)
	}
	if store.pol != nil && store.pol.TemplateKey == "pt" {
		t.Fatal("a failed list preparation still created the policy")
	}
	// The store's own refusals pass through (a second copy, a name taken).
	delete(lists.err, "category-pt")
	store.err = fmt.Errorf("%w: template %q was already added", domain.ErrAlreadyExists, "pt")
	if _, err := svc.Instantiate(ctx, "pt", "en-US", domain.DestStateOn); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("a second copy: %v", err)
	}
}

func TestView_MarksTheTemplatesAlreadyAdded(t *testing.T) {
	svc, _, store := newService(t)
	store.added = []*domain.DestPolicy{
		{ID: 3, Name: "my own", TemplateKey: ""},
		{ID: 9, Name: "BitTorrent (edited)", TemplateKey: "bittorrent", State: domain.DestStateOn},
	}
	v, err := svc.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 1 || len(v.Templates) != 13 || len(v.Unavailable) != 3 {
		t.Fatalf("view = version %d, %d templates, %d unavailable", v.Version, len(v.Templates), len(v.Unavailable))
	}
	for _, tv := range v.Templates {
		switch tv.Key {
		case "bittorrent":
			if !tv.Added || tv.PolicyID != 9 || tv.PolicyState != domain.DestStateOn {
				t.Errorf("bittorrent = %+v", tv)
			}
		default:
			if tv.Added || tv.PolicyID != 0 {
				t.Errorf("%s reads as added: %+v", tv.Key, tv)
			}
		}
	}
	// The view hands out copies: editing it cannot change the catalog.
	v.Templates[0].Name["zh-CN"] = "tampered"
	again, _ := svc.View(context.Background())
	if again.Templates[0].Name["zh-CN"] == "tampered" {
		t.Fatal("the view shares the catalog's maps")
	}
}

// The lists are derived from the current geosite data and stored in one
// critical section, so an update cannot swap the data between the two; the
// download Ensure may trigger happens BEFORE the lock (it takes it itself).
func TestInstantiate_HoldsTheGeositeLockAcrossDerivationAndStorage(t *testing.T) {
	svc, lists, store := newService(t)
	var events []string
	lists.events, store.events = &events, &events
	if _, err := svc.Instantiate(context.Background(), "doh", "en-US", domain.DestStateOff); err != nil {
		t.Fatal(err)
	}
	want := []string{"ensure", "lock", "spec", "store", "unlock"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	// A failure inside still releases it.
	events = nil
	store.err = errors.New("boom")
	_, _ = svc.Instantiate(context.Background(), "doh", "en-US", domain.DestStateOff)
	if events[len(events)-1] != "unlock" {
		t.Fatalf("the lock was not released after a failure: %v", events)
	}
}

// A hit on BitTorrent or an outgoing mail port is evidence about the account, so
// those two templates create a policy that counts as a risk signal; a category
// list or the private-address match (which a node blocks anyway) does not.
func TestInstantiate_OnlyBitTorrentAndMailPortsCountAsRiskByDefault(t *testing.T) {
	svc, _, store := newService(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range c.Templates {
		want := tpl.Key == "bittorrent" || tpl.Key == "smtp"
		if tpl.CountsAsRisk != want {
			t.Errorf("template %s counts_as_risk = %v, want %v", tpl.Key, tpl.CountsAsRisk, want)
		}
		if _, err := svc.Instantiate(context.Background(), tpl.Key, "en-US", domain.DestStateReport); err != nil {
			t.Fatal(err)
		}
		if store.pol.CountsAsRisk != want {
			t.Errorf("the policy made from %s has counts_as_risk = %v, want %v", tpl.Key, store.pol.CountsAsRisk, want)
		}
	}
}
