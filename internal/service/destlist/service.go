package destlist

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The narrow views of the stores this service uses. Each is declared here, by
// its consumer, and satisfied by the concrete sqlstore repos; none is a
// ports.Repos field.

// ListStore is the dest_lists store.
type ListStore interface {
	Create(ctx context.Context, l *domain.DestList) error
	GetByID(ctx context.Context, id int64) (*domain.DestList, error)
	List(ctx context.Context, withEntries bool) ([]*domain.DestList, error)
	Entries(ctx context.Context, id int64) ([]byte, error)
	Update(ctx context.Context, l *domain.DestList) error
	UpdateContent(ctx context.Context, id int64, c domain.DestListContent) (bool, error)
	SetFetchError(ctx context.Context, id int64, reason string) error
	FindGeosite(ctx context.Context, category, attrs string) (*domain.DestList, error)
	ApplyBatch(ctx context.Context, batch domain.DestListBatch) ([]int64, bool, error)
	DeleteUnused(ctx context.Context, id int64) (using domain.DestListUse, err error)
}

// PolicyReader is what the list service needs of the policies: which lists are
// in force, to judge an update against them, and which reference one, to refuse
// deleting it.
type PolicyReader interface {
	List(ctx context.Context) ([]*domain.DestPolicy, error)
	UsingList(ctx context.Context, listID int64) ([]int64, error)
}

// GroupAllowlistReader reads the allowlist groups (sqlstore.DestGroupAllowlistRepo):
// which of them hold a list, to say so in "used by" and to keep a list an "on"
// group holds from being edited into one that is not ready.
type GroupAllowlistReader interface {
	List(ctx context.Context) ([]*domain.DestGroupAllowlistEntry, error)
}

// SourceRuleReader reads the per-node source rules, to know whether a GeoIP
// change matters.
type SourceRuleReader interface {
	List(ctx context.Context) ([]*domain.DestSourceRule, error)
}

// StateStore is the publish bookkeeping the service reads and writes: the
// persisted digest of the GeoIP database the policy was last reconciled with,
// and the generation bump that goes with a change of it.
type StateStore interface {
	Get(ctx context.Context) (*domain.DestPolicyState, error)
	ReconcileGeoIP(ctx context.Context, sha string, bump bool) error
}

// SettingsReader is the slice of the settings store the loop reads each round.
type SettingsReader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
}

// Deps is everything a Service is built from. Lists, Policies and DataDir are
// required; every other collaborator may be nil, and the feature it serves then
// degrades: no SourceRules or State means a GeoIP database change cannot be
// noticed, no Settings means the defaults, no Locator means country expansion
// reports ErrGeoIPUnavailable.
type Deps struct {
	Lists       ListStore
	Policies    PolicyReader
	SourceRules SourceRuleReader
	State       StateStore
	// Groups: nil, no group is known to hold a list (the list is then judged by
	// the policies alone).
	Groups   GroupAllowlistReader
	Settings SettingsReader
	Locator  MMDBLocator
	// DataDir is the panel's data directory; geosite files live in
	// <DataDir>/destlists.
	DataDir string
	// HTTP fetches remote lists and the geosite release; nil is the
	// SSRF-refusing client.
	HTTP Doer
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// ObserveRefresh is told the result of each refresh ("unchanged",
	// "updated", "failed", "broad"): the metrics hook.
	ObserveRefresh func(result string)
}

// Service owns the lists.
type Service struct {
	lists       ListStore
	policies    PolicyReader
	sourceRules SourceRuleReader
	state       StateStore
	groups      GroupAllowlistReader
	settings    SettingsReader
	doer        Doer
	now         func() time.Time
	observe     func(string)

	// Countries expands GeoIP countries; exported so the policy compiler reads
	// it through the one cache.
	Countries *CountryExpander

	geo geositeStore

	// refreshMu makes the refresh single-flight; a second RefreshDue while one
	// runs returns at once.
	refreshMu sync.Mutex
	// entriesMu serialises AddEntries' read-modify-write of a list's text.
	entriesMu sync.Mutex
	// refreshing marks the lists an admin asked to refresh that are still
	// running (BeginRefresh), for the page's "refreshing" state.
	refreshingMu sync.Mutex
	refreshing   map[int64]time.Time

	// afterSwap is a test seam: called with the files swapped and the lists
	// not yet committed.
	afterSwap func()

	// autoTag/autoAt remember the release the last automatic update tried and
	// when, guarded by geo.mu.
	autoTag string
	autoAt  time.Time
}

// New builds the service. It touches no disk and starts nothing: the geosite
// state is read on first use.
func New(d Deps) *Service {
	doer := d.HTTP
	if doer == nil {
		doer = newSafeClient()
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	observe := d.ObserveRefresh
	if observe == nil {
		observe = func(string) {}
	}
	s := &Service{
		lists: d.Lists, policies: d.Policies, sourceRules: d.SourceRules, state: d.State, groups: d.Groups, settings: d.Settings,
		doer: doer, now: now, observe: observe, Countries: NewCountryExpander(d.Locator),
	}
	s.geo = geositeStore{
		dir: filepath.Join(d.DataDir, "destlists"), doer: doer, now: now,
		apiURL: geositeLatestURL, downloadBase: geositeDownloadBase,
	}
	return s
}

// ---- lists ----

// ErrListInUse refuses deleting a list a policy references (HTTP 409
// dest_list_in_use).
var ErrListInUse = fmt.Errorf("%w: dest list is in use by a policy", domain.ErrConflict)

// ListInUseError names the policies that hold a list.
type ListInUseError struct{ PolicyIDs, GroupIDs []int64 }

func (e *ListInUseError) Error() string {
	return fmt.Sprintf("%v: policies %v, groups %v", ErrListInUse, e.PolicyIDs, e.GroupIDs)
}
func (e *ListInUseError) Unwrap() error { return ErrListInUse }

// ErrListNotReady refuses a change that would leave a list an "on" allowlist
// group holds not ready (HTTP 409 dest_list_not_ready): the allowlist would
// deny everything the list names.
var ErrListNotReady = domain.ErrDestListNotReady

// ListNotReadyError names the lists that are not ready, and the groups that
// hold them.
type ListNotReadyError = domain.DestListNotReadyError

// HoldsList reports whether the allowlist names list id: among its lists, or as
// its base or extra list.
func HoldsList(a domain.DestAllowlist, id int64) bool {
	return a.BaseListID == id || a.ExtraListID == id || slices.Contains(a.ListIDs, id)
}

// requireReadyForOnGroups refuses l (as it would be saved) when it is not ready
// and an "on" group holds it.
func (s *Service) requireReadyForOnGroups(ctx context.Context, l *domain.DestList) error {
	if l.Ready() || s.groups == nil {
		return nil
	}
	entries, err := s.groups.List(ctx)
	if err != nil {
		return err
	}
	var groups []int64
	for _, e := range entries {
		if e.Allowlist.State == domain.DestStateOn && HoldsList(e.Allowlist, l.ID) {
			groups = append(groups, e.GroupID)
		}
	}
	if len(groups) > 0 {
		return &ListNotReadyError{ListIDs: []int64{l.ID}, GroupIDs: groups}
	}
	return nil
}

func contentOf(p *Parsed, at time.Time) domain.DestListContent {
	return domain.DestListContent{
		Entries: p.Entries, EntryCount: p.EntryCount, RegexpCount: p.RegexpCount, ContentSHA256: p.SHA256, FetchedAt: at,
	}
}

func checkName(name string) error {
	if !domain.ValidDestName(name) {
		return fmt.Errorf("%w: dest list: the name must be 1..%d bytes without surrounding space", domain.ErrValidation, domain.DestNameMax)
	}
	return nil
}

// CreateCustom parses pasted text and stores it as a list. The pasted text is
// kept verbatim (comments, order and all) for the editor to show again; the
// canonical entries are what the policy uses.
func (s *Service) CreateCustom(ctx context.Context, name string, side domain.DestListSide, text []byte) (*domain.DestList, *ParseReport, error) {
	if side == "" {
		side = domain.DestSideDest
	}
	if err := checkName(name); err != nil {
		return nil, nil, err
	}
	p, err := ParseCustom(text, side)
	if err != nil {
		return nil, nil, err
	}
	now := s.now().UTC()
	l := &domain.DestList{
		Name: name, Kind: domain.DestListCustom, Side: side, Entries: p.Entries, SourceText: slices.Clone(text),
		EntryCount: p.EntryCount, RegexpCount: p.RegexpCount, ContentSHA256: p.SHA256, LastFetchedAt: &now,
	}
	if err := s.lists.Create(ctx, l); err != nil {
		return nil, nil, err
	}
	return l, &p.Report, nil
}

// UpdateCustom rewrites a custom list's name and text. A remote or geosite
// list has no text to edit.
func (s *Service) UpdateCustom(ctx context.Context, id int64, name string, text []byte) (*domain.DestList, *ParseReport, error) {
	if err := checkName(name); err != nil {
		return nil, nil, err
	}
	l, err := s.lists.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if l.Kind != domain.DestListCustom {
		return nil, nil, fmt.Errorf("%w: only a custom list has text to edit", domain.ErrValidation)
	}
	p, err := ParseCustom(text, l.Side)
	if err != nil {
		return nil, nil, err
	}
	now := s.now().UTC()
	l.Name, l.SourceText, l.Entries = name, slices.Clone(text), p.Entries
	l.EntryCount, l.RegexpCount, l.ContentSHA256, l.LastFetchedAt, l.LastError = p.EntryCount, p.RegexpCount, p.SHA256, &now, ""
	if err := s.lists.Update(ctx, l); err != nil {
		return nil, nil, err
	}
	return l, &p.Report, nil
}

// CreateRemote fetches rawURL and stores it as a list. The first fetch is part
// of creating: a URL that does not answer, answers too much, or does not parse
// creates nothing, so the admin sees the reason where they typed the URL
// instead of finding a list that never arrived.
func (s *Service) CreateRemote(ctx context.Context, name, rawURL string, side domain.DestListSide) (*domain.DestList, *ParseReport, error) {
	if side == "" {
		side = domain.DestSideDest
	}
	if err := checkName(name); err != nil {
		return nil, nil, err
	}
	u, err := ValidateRemoteURL(rawURL)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.fetchParsed(ctx, u.String(), side)
	if err != nil {
		return nil, nil, err
	}
	now := s.now().UTC()
	l := &domain.DestList{
		Name: name, Kind: domain.DestListRemote, Side: side, SourceURL: u.String(), Entries: p.Entries,
		EntryCount: p.EntryCount, RegexpCount: p.RegexpCount, ContentSHA256: p.SHA256, LastFetchedAt: &now,
	}
	if err := s.lists.Create(ctx, l); err != nil {
		return nil, nil, err
	}
	return l, &p.Report, nil
}

func (s *Service) fetchParsed(ctx context.Context, rawURL string, side domain.DestListSide) (*Parsed, error) {
	body, err := fetchBody(ctx, s.doer, rawURL, MaxRemoteBytes)
	if err != nil {
		return nil, err
	}
	return ParseRemote(body, side)
}

// Refresh results, the metric's result label.
const (
	RefreshUnchanged = "unchanged"
	RefreshUpdated   = "updated"
	RefreshFailed    = "failed"
	RefreshBroad     = "broad"
)

// RefreshOutcome is what one refresh did.
type RefreshOutcome struct {
	Result string       `json:"result"`
	Error  string       `json:"error,omitempty"`
	Report *ParseReport `json:"report,omitempty"`
}

// RefreshList fetches a remote list again. A failure (the fetch, the parse, a
// broad entry, a list over its limit) keeps the previous entries and records
// why in last_error; success replaces the entries only when their digest
// changed, and advances the policy generation only then.
func (s *Service) RefreshList(ctx context.Context, id int64) (RefreshOutcome, error) {
	l, err := s.lists.GetByID(ctx, id)
	if err != nil {
		return RefreshOutcome{}, err
	}
	if l.Kind != domain.DestListRemote {
		return RefreshOutcome{}, fmt.Errorf("%w: only a remote list is refreshed from a URL", domain.ErrValidation)
	}
	return s.refreshRemote(ctx, l), nil
}

func (s *Service) refreshRemote(ctx context.Context, l *domain.DestList) RefreshOutcome {
	fail := func(result string, err error) RefreshOutcome {
		reason := err.Error()
		var big *TooLargeError
		var broad *BroadEntryError
		switch {
		case errors.As(err, &broad):
			reason = "broad_entry: " + broad.Entry
		case errors.As(err, &big):
			reason = "dest_list_too_large"
		}
		if serr := s.lists.SetFetchError(ctx, l.ID, reason); serr != nil {
			log.Warn("destlist: record refresh error failed", "list_id", l.ID, "err", serr)
		}
		s.observe(result)
		return RefreshOutcome{Result: result, Error: reason}
	}
	p, err := s.fetchParsed(ctx, l.SourceURL, l.Side)
	if err != nil {
		var broad *BroadEntryError
		if errors.As(err, &broad) {
			return fail(RefreshBroad, err)
		}
		return fail(RefreshFailed, err)
	}
	if p.EntryCount == 0 {
		// A feed that went empty must not blank the list an "on" allowlist is made
		// of (D5): the refresh fails and the previous entries stay.
		now := s.now().UTC()
		if err := s.requireReadyForOnGroups(ctx, &domain.DestList{ID: l.ID, Kind: l.Kind, LastFetchedAt: &now}); err != nil {
			return fail(RefreshFailed, err)
		}
	}
	changed, err := s.lists.UpdateContent(ctx, l.ID, contentOf(p, s.now()))
	if err != nil {
		return fail(RefreshFailed, err)
	}
	result := RefreshUnchanged
	if changed {
		result = RefreshUpdated
	}
	s.observe(result)
	return RefreshOutcome{Result: result, Report: &p.Report}
}

// DeleteList removes a list no policy uses. The check and the delete are one
// transaction: a policy cannot pick the list up in between.
func (s *Service) DeleteList(ctx context.Context, id int64) error {
	using, err := s.lists.DeleteUnused(ctx, id)
	if err != nil {
		return err
	}
	if using.Any() {
		return &ListInUseError{PolicyIDs: using.PolicyIDs, GroupIDs: using.GroupIDs}
	}
	return nil
}

// GetList reads one list with its entries.
func (s *Service) GetList(ctx context.Context, id int64) (*domain.DestList, error) {
	return s.lists.GetByID(ctx, id)
}

// Lists reads every list without its large columns.
func (s *Service) Lists(ctx context.Context) ([]*domain.DestList, error) {
	return s.lists.List(ctx, false)
}

// Entries reads one list's canonical entries.
func (s *Service) Entries(ctx context.Context, id int64) ([]byte, error) {
	return s.lists.Entries(ctx, id)
}

// ---- expansion ----

// Expanded is a list's entries in the form the wire policy carries.
type Expanded struct {
	// Domains are "domain:", "full:", "keyword:" and "regexp:" entries, with
	// every geosite reference replaced by the category's entries.
	Domains []string
	// CIDRs are address ranges: the list's own, and every GeoIP country in it
	// expanded to its ranges. On a source list they are SOURCE ranges.
	CIDRs []string
}

// ErrGeositeUnavailable means a list references a geosite category the
// current data does not have (or there is no data yet).
var ErrGeositeUnavailable = fmt.Errorf("%w: geosite data unavailable", domain.ErrUnavailable)

// Expand resolves a list's entries for the compiler: plain entries as they
// are, geosite references from the current geosite data, GeoIP countries from
// the GeoIP database. A reference that cannot be resolved is an error
// (ErrGeositeUnavailable, ErrGeoIPUnavailable), never an empty expansion: a
// list that quietly matches less than it says is the failure this prevents.
// Both slices are sorted and without repeats.
func (s *Service) Expand(ctx context.Context, l *domain.DestList) (*Expanded, error) {
	var domains, cidrs []string
	var countries []string
	for _, e := range strings.Split(string(l.Entries), "\n") {
		switch {
		case e == "":
		case strings.HasPrefix(e, "geosite:"):
			name, attrs, ok := splitGeosite(strings.TrimPrefix(e, "geosite:"))
			if !ok {
				continue
			}
			d, err := s.DeriveGeosite(ctx, name, attrs)
			if err != nil {
				return nil, err
			}
			domains = append(domains, strings.Split(string(d.Entries), "\n")...)
		case strings.HasPrefix(e, "geoip:"):
			countries = append(countries, strings.TrimPrefix(e, "geoip:"))
		case strings.HasPrefix(e, "domain:"), strings.HasPrefix(e, "full:"), strings.HasPrefix(e, "keyword:"), strings.HasPrefix(e, "regexp:"):
			domains = append(domains, e)
		default:
			cidrs = append(cidrs, e)
		}
	}
	if len(countries) > 0 {
		more, err := s.Countries.CIDRs(ctx, countries)
		if err != nil {
			return nil, err
		}
		cidrs = append(cidrs, more...)
	}
	// Re-merge: the list's own ranges and a country's may overlap.
	var ps []netip.Prefix
	for _, c := range cidrs {
		if p, err := netip.ParsePrefix(c); err == nil {
			ps = append(ps, p)
		}
	}
	merged := MergePrefixes(ps)
	cidrs = cidrs[:0]
	for _, p := range merged {
		cidrs = append(cidrs, p.String())
	}
	slices.Sort(cidrs)
	slices.Sort(domains)
	return &Expanded{Domains: slices.Compact(domains), CIDRs: cidrs}, nil
}
