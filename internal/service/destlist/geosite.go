package destlist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Geosite data: the v2fly domain-list-community release that geosite lists are
// derived from.
//
// WHY THE PANEL EXPANDS IT. Nodes carry no geosite.dat (F12): the panel
// downloads the release's plain YAML, expands each category into literal
// entries and ships those, so every node and both cores (Xray and sing-box)
// run the very same entries, and "which data version is a node on" is answered
// by the digest of the policy it reports, with no new wire field.
//
// The upstream is v2fly/domain-list-community only: the canonical source,
// released daily with a timestamp tag. The downstreams that repackage it add a
// layer that can stall or change shape, and sing-geosite is not needed because
// nothing here ships a .dat or an .srs.

const (
	geositeLatestURL    = "https://api.github.com/repos/v2fly/domain-list-community/releases/latest"
	geositeDownloadBase = "https://github.com/v2fly/domain-list-community/releases/download"
	geositeAsset        = "dlc.dat_plain.yml"

	// geositeThrottle is how often the upstream is asked for its latest tag.
	// New releases land daily and the badge may lag by hours; the longer
	// window keeps the panel far under GitHub's anonymous rate limit.
	geositeThrottle = 6 * time.Hour
	// geositeProbeBackoff spaces retries after a FAILED probe, so an outage
	// does not turn every refresh round into a request.
	geositeProbeBackoff = 10 * time.Minute

	// maxGeositeBytes bounds the release file (3.8 MB today). Over it the
	// download fails; it never truncates.
	maxGeositeBytes = 32 << 20
	maxProbeBytes   = 1 << 20
	maxShaFileBytes = 4 << 10

	stateFile   = "state.json"
	currentFile = "dlc_plain.yml"
	prevFile    = "dlc_plain.prev.yml"
)

var geositeTagRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// GeositeMeta identifies one downloaded release.
type GeositeMeta struct {
	Tag          string    `json:"tag"`
	SHA256       string    `json:"sha256"`
	DownloadedAt time.Time `json:"downloaded_at"`
	Bytes        int64     `json:"bytes"`
}

// ReleasedAt is the release's time, read from its tag: v2fly tags are the UTC
// build time, YYYYMMDDHHMMSS. nil for a tag of another shape.
func (m GeositeMeta) ReleasedAt() *time.Time { return tagTime(m.Tag) }

func tagTime(tag string) *time.Time {
	if len(tag) != 14 {
		return nil
	}
	t, err := time.ParseInLocation("20060102150405", tag, time.UTC)
	if err != nil {
		return nil
	}
	return &t
}

// geositeState is what survives a restart: the two kept releases and the last
// upstream probe.
type geositeState struct {
	Current  *GeositeMeta `json:"current,omitempty"`
	Previous *GeositeMeta `json:"previous,omitempty"`

	// Pending is written (and fsynced) BEFORE the lists commit and cleared
	// after: while it is set a swap is in flight, and a start heals it from
	// what the database says about the release.
	Pending *geositePending `json:"pending,omitempty"`

	LatestTag       string    `json:"latest_tag,omitempty"`
	LatestCheckedAt time.Time `json:"latest_checked_at,omitempty"`
	LatestError     string    `json:"latest_error,omitempty"`
	// LastUpdateError is why the last update was refused ("" after a success).
	LastUpdateError   string    `json:"last_update_error,omitempty"`
	LastUpdateErrorAt time.Time `json:"last_update_error_at,omitempty"`
}

// geositePending is a swap in flight: the operation, the release moving in
// and the one it replaces.
type geositePending struct {
	Op   string       `json:"op"` // "update" or "rollback"
	To   GeositeMeta  `json:"to"`
	From *GeositeMeta `json:"from,omitempty"`
}

// geositeStore holds the files and the in-memory copy of the current release.
type geositeStore struct {
	dir          string
	doer         Doer
	now          func() time.Time
	apiURL       string
	downloadBase string

	mu           sync.Mutex
	loaded       bool
	st           geositeState
	data         *dlcData
	dataErr      string
	lastProbeTry time.Time

	// updateMu serialises update and rollback (single-flight).
	updateMu sync.Mutex
}

func (g *geositeStore) path(name string) string { return filepath.Join(g.dir, name) }

// rollbackSuffix names the file a swap sets the previous current release aside
// under until the lists have committed.
const rollbackSuffix = ".rollback"

// loadLocked reads the state and the current release once. A current file that
// is missing or whose digest no longer matches its record is NOT used and is
// reported (dataErr): serving data that is not what the record says would make
// the version shown a lie.
//
// A swap sets the old current file aside (rollbackSuffix) and records a
// pending marker in state.json before the lists commit; a start that finds the
// marker heals it (Service.heal) rather than guessing from the files.
func (g *geositeStore) loadLocked() {
	if g.loaded {
		return
	}
	g.loaded = true
	if b, err := os.ReadFile(g.path(stateFile)); err == nil {
		if err := json.Unmarshal(b, &g.st); err != nil {
			log.Warn("destlist: geosite state unreadable; starting without it", "err", err)
			g.st = geositeState{}
		}
	}
	if g.st.Pending != nil {
		// A swap was in flight. The files may be in either state, so nothing
		// is read until heal has asked the database which way it went.
		g.dataErr = "geosite_pending"
		return
	}
	_ = os.Remove(g.path(currentFile + ".new"))
	g.readDataLocked()
}

// readDataLocked reads and verifies the current release file.
func (g *geositeStore) readDataLocked() {
	g.data, g.dataErr = nil, ""
	if g.st.Current == nil {
		return
	}
	raw, err := os.ReadFile(g.path(currentFile))
	if err != nil {
		g.dataErr = "geosite_data_missing"
		return
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != g.st.Current.SHA256 {
		g.dataErr = "geosite_data_corrupt"
		return
	}
	d, err := parseDLC(raw)
	if err != nil {
		g.dataErr = "geosite_data_corrupt"
		return
	}
	g.data = d
}

func (g *geositeStore) saveLocked() error {
	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(g.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileDurable(g.path(stateFile), b)
}

// writeFileDurable writes to a sibling temp file, fsyncs it, renames it over
// path and fsyncs the directory: a crash leaves the old file or the new, never
// half of one, and never a rename the disk has not seen.
func writeFileDurable(path string, b []byte) error {
	tmp := fmt.Sprintf("%s.part.%d", path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(path))
}

// syncDir fsyncs a directory so a rename in it is durable. Platforms that
// cannot open a directory for sync (Windows) are not an error.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	_ = d.Sync()
	return nil
}

// current returns the parsed current release and its record, nil when there
// is none (or it could not be used).
func (g *geositeStore) current() (*dlcData, *GeositeMeta) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadLocked()
	if g.data == nil {
		return nil, nil
	}
	m := *g.st.Current
	return g.data, &m
}

// heal resolves a swap an earlier process left in flight. The release the
// geosite lists were last derived from is recorded in the database in the
// same transaction as the lists, so it says which way the interrupted commit
// went: if it equals the pending release the commit happened and the swap is
// completed; otherwise it did not and the files are put back. The previous
// release survives either way (a rollback's file moves are undone move for
// move). It runs before anything reads or changes the files, and is retried
// until the database can be asked.
func (s *Service) heal(ctx context.Context) error {
	// Only when no operation of THIS process owns the files. An update in
	// flight has a pending record too, and healing it would undo a swap whose
	// commit is about to land; while one runs, the in-memory state (the
	// release before the update, until it commits) is what is served.
	if !s.geo.updateMu.TryLock() {
		return nil
	}
	defer s.geo.updateMu.Unlock()
	return s.healLocked(ctx)
}

// healLocked is heal for a caller that already holds updateMu (the update and
// rollback, and the geosite lock).
func (s *Service) healLocked(ctx context.Context) error {
	g := &s.geo
	g.mu.Lock()
	g.loadLocked()
	if g.st.Pending == nil {
		g.mu.Unlock()
		return nil
	}
	pend := *g.st.Pending
	g.mu.Unlock()
	if s.state == nil {
		return errors.New("a geosite swap was interrupted and the database cannot be asked how it ended")
	}
	st, err := s.state.Get(ctx)
	if err != nil {
		return fmt.Errorf("a geosite swap was interrupted and the database cannot be asked how it ended: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.st.Pending == nil {
		return nil
	}
	cur, prev, aside := g.path(currentFile), g.path(prevFile), g.path(currentFile+rollbackSuffix)
	// THE TEST FOR "COMMITTED" IS TAG EQUALITY, and it is sound because the
	// release the lists reflect can never already equal the release a pending
	// swap is moving TO: an update to the release already current returns
	// early, a rollback's target is by construction not the current release,
	// and a completed swap clears the record. So geosite_release == pending.To
	// can only mean that THIS attempt's transaction committed (it is written in
	// that same transaction). Were that ever to change, the record would need
	// an attempt id.
	if st.GeositeRelease == pend.To.Tag {
		// Committed: the new release is current, the old one becomes previous.
		if _, err := os.Stat(aside); err == nil {
			if err := os.Rename(aside, prev); err != nil {
				return err
			}
		}
		g.st.Previous = pend.From
		t := pend.To
		g.st.Current = &t
		log.Warn("destlist: completed a geosite swap an earlier process left in flight", "tag", pend.To.Tag)
	} else {
		// Not committed: put every file back where it was.
		if _, err := os.Stat(aside); err == nil {
			if _, err := os.Stat(cur); err == nil {
				if pend.Op == "rollback" {
					if err := os.Rename(cur, prev); err != nil {
						return err
					}
				} else if err := os.Remove(cur); err != nil {
					return err
				}
			}
			if err := os.Rename(aside, cur); err != nil {
				return err
			}
		} else if g.st.Current == nil {
			_ = os.Remove(cur) // a first install that never committed
		}
		_ = os.Remove(g.path(currentFile + ".new"))
		log.Warn("destlist: restored the geosite files after an interrupted swap", "tag", pend.To.Tag)
	}
	_ = syncDir(g.dir)
	g.st.Pending = nil
	if err := g.saveLocked(); err != nil {
		return err
	}
	g.readDataLocked()
	return nil
}

// currentGeo is current() after healing; a heal that cannot run is logged and
// the answer is "no usable data", never data read from files in an unknown
// state.
func (s *Service) currentGeo(ctx context.Context) (*dlcData, *GeositeMeta) {
	if err := s.heal(ctx); err != nil {
		log.Warn("destlist: geosite state needs healing", "err", err)
		return nil, nil
	}
	return s.geo.current()
}

// ---- upstream ----

// GeositeLatest is the last answer of the upstream probe.
type GeositeLatest struct {
	Tag        string     `json:"tag"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// CheckLatest asks GitHub for the newest release tag, at most once per six
// hours unless force. It is the pattern of version.RefreshLatestXUI —
// safehttp client, throttle — with the result kept in state.json so a restart
// does not spend a request. A failure keeps the previous answer and records
// the error.
func (s *Service) CheckLatest(ctx context.Context, force bool) (GeositeLatest, error) {
	g := &s.geo
	g.mu.Lock()
	g.loadLocked()
	now := g.now()
	if !force && !g.st.LatestCheckedAt.IsZero() && now.Sub(g.st.LatestCheckedAt) < geositeThrottle && g.st.LatestTag != "" {
		out := g.latestLocked()
		g.mu.Unlock()
		return out, nil
	}
	if !force && !g.lastProbeTry.IsZero() && now.Sub(g.lastProbeTry) < geositeProbeBackoff && g.st.LatestError != "" {
		out := g.latestLocked()
		g.mu.Unlock()
		return out, nil
	}
	g.lastProbeTry = now
	g.mu.Unlock()

	tag, err := g.probe(ctx)

	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		g.st.LatestError = err.Error()
		_ = g.saveLocked()
		return g.latestLocked(), err
	}
	g.st.LatestTag, g.st.LatestCheckedAt, g.st.LatestError = tag, g.now().UTC(), ""
	if err := g.saveLocked(); err != nil {
		log.Warn("destlist: saving the geosite state failed", "err", err)
	}
	return g.latestLocked(), nil
}

func (g *geositeStore) latestLocked() GeositeLatest {
	out := GeositeLatest{Tag: g.st.LatestTag, Error: g.st.LatestError, ReleasedAt: tagTime(g.st.LatestTag)}
	if !g.st.LatestCheckedAt.IsZero() {
		t := g.st.LatestCheckedAt
		out.CheckedAt = &t
	}
	return out
}

func (g *geositeStore) probe(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := g.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch latest geosite release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch latest geosite release: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBytes))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("decode release JSON: %w", err)
	}
	if !geositeTagRE.MatchString(rel.TagName) {
		return "", fmt.Errorf("unusable tag_name %q", rel.TagName)
	}
	return rel.TagName, nil
}

// download fetches one release's plain YAML and its .sha256sum and verifies
// the first against the second BEFORE anything is parsed (F47). The tag is
// checked against a strict pattern first: it becomes part of a URL.
func (g *geositeStore) download(ctx context.Context, tag string) (raw []byte, sum string, err error) {
	if !geositeTagRE.MatchString(tag) {
		return nil, "", fmt.Errorf("%w: unusable geosite release tag", domain.ErrValidation)
	}
	base := g.downloadBase + "/" + tag + "/" + geositeAsset
	shaBody, err := fetchBody(ctx, g.doer, base+".sha256sum", maxShaFileBytes)
	if err != nil {
		return nil, "", fmt.Errorf("checksum file: %w", err)
	}
	want, err := parseSha256sum(shaBody)
	if err != nil {
		return nil, "", fmt.Errorf("checksum file: %w", err)
	}
	raw, err = fetchBody(ctx, g.doer, base, maxGeositeBytes)
	if err != nil {
		return nil, "", fmt.Errorf("download: %w", err)
	}
	got := sha256.Sum256(raw)
	if hex.EncodeToString(got[:]) != want {
		return nil, "", fmt.Errorf("%w: the downloaded geosite data does not match its published sha256", domain.ErrValidation)
	}
	return raw, want, nil
}

// parseSha256sum reads "<64 hex>  <name>" (the sha256sum format) and returns
// the digest.
func parseSha256sum(b []byte) (string, error) {
	fields := strings.Fields(string(b))
	if len(fields) == 0 || !isHex64(fields[0]) {
		return "", errors.New("not a sha256sum line")
	}
	if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") != geositeAsset {
		return "", fmt.Errorf("checksum is for %q, not %s", fields[1], geositeAsset)
	}
	return strings.ToLower(fields[0]), nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range strings.ToLower(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ---- status ----

// GeositeStatus is the data-version panel: what is in use, what is kept for
// rollback, what the upstream offers.
type GeositeStatus struct {
	Current  *GeositeMeta `json:"current,omitempty"`
	Previous *GeositeMeta `json:"previous,omitempty"`
	// CurrentReleasedAt / CurrentAgeDays describe the release in use, from its
	// tag; BehindDays is how much older it is than the latest one.
	CurrentReleasedAt *time.Time    `json:"current_released_at,omitempty"`
	CurrentAgeDays    *int          `json:"current_age_days,omitempty"`
	Latest            GeositeLatest `json:"latest"`
	UpdateAvailable   bool          `json:"update_available"`
	BehindDays        *int          `json:"behind_days,omitempty"`
	Mode              string        `json:"mode"`
	// DataError is set when the current release could not be used
	// ("geosite_data_missing", "geosite_data_corrupt").
	DataError       string `json:"data_error,omitempty"`
	LastUpdateError string `json:"last_update_error,omitempty"`
}

// GeositeStatus reports the data versions. It never touches the network: the
// probe is the refresh loop's (and CheckLatest's).
func (s *Service) GeositeStatus(ctx context.Context) GeositeStatus {
	g := &s.geo
	if err := s.heal(ctx); err != nil {
		log.Warn("destlist: geosite state needs healing", "err", err)
	}
	g.mu.Lock()
	g.loadLocked()
	st := GeositeStatus{Latest: g.latestLocked(), DataError: g.dataErr, LastUpdateError: g.st.LastUpdateError}
	if g.st.Current != nil {
		c := *g.st.Current
		st.Current = &c
	}
	if g.st.Previous != nil {
		p := *g.st.Previous
		st.Previous = &p
	}
	g.mu.Unlock()

	st.Mode = s.rulesUpdateMode(ctx)
	if st.Current != nil {
		if rel := st.Current.ReleasedAt(); rel != nil {
			st.CurrentReleasedAt = rel
			age := int(g.now().Sub(*rel).Hours() / 24)
			st.CurrentAgeDays = &age
			if latest := tagTime(st.Latest.Tag); latest != nil && latest.After(*rel) {
				behind := int(latest.Sub(*rel).Hours() / 24)
				st.BehindDays = &behind
			}
		}
	}
	st.UpdateAvailable = st.Latest.Tag != "" && (st.Current == nil || st.Latest.Tag != st.Current.Tag)
	return st
}

func (s *Service) rulesUpdateMode(ctx context.Context) string {
	if s.settings == nil {
		return ports.DestRulesUpdateManual
	}
	set, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return ports.DestRulesUpdateManual
	}
	return ports.AccessControlEffectiveOf(set).RulesUpdateMode
}

// GeositeCategories is the category index of the current release, with the
// literal attributes each carries.
func (s *Service) GeositeCategories(ctx context.Context) ([]Category, error) {
	d, _ := s.currentGeo(ctx)
	if d == nil {
		return nil, ErrGeositeUnavailable
	}
	return d.categories(), nil
}

// DeriveGeosite expands one (category, attrs) selection from the current
// release.
func (s *Service) DeriveGeosite(ctx context.Context, category string, attrs []string) (*Derived, error) {
	d, _ := s.currentGeo(ctx)
	if d == nil {
		return nil, ErrGeositeUnavailable
	}
	res, ok := d.derive(category, attrs)
	if !ok {
		return nil, fmt.Errorf("%w: geosite category %q is not in the current data", ErrGeositeUnavailable, category)
	}
	return res, nil
}

// ---- geosite lists ----

func geositeListName(category string, attrs []string) string {
	return strings.TrimPrefix(FormatGeositeRef(category, attrs), "geosite:")
}

// GeositeListSpec builds (without saving) the list for a category and
// attribute filter from the current data. The selection must exist, match at
// least one entry, hold no broad entry and fit a list's limits: each of these
// is refused here, at the moment of choosing, rather than at publish.
func (s *Service) GeositeListSpec(ctx context.Context, category string, attrs []string) (domain.DestList, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	norm := slices.Compact(slices.Sorted(slices.Values(attrs)))
	d, err := s.DeriveGeosite(ctx, category, norm)
	if err != nil {
		return domain.DestList{}, err
	}
	switch {
	case d.EntryCount == 0:
		return domain.DestList{}, fmt.Errorf("%w: geosite %s matches no entries", domain.ErrValidation, geositeListName(category, norm))
	case d.Broad != "":
		return domain.DestList{}, fmt.Errorf("%w: geosite %s holds a broad entry (%s)", domain.ErrValidation, geositeListName(category, norm), d.Broad)
	case d.EntryCount > MaxListEntries:
		return domain.DestList{}, &TooLargeError{Kind: "entries", Used: d.EntryCount, Limit: MaxListEntries}
	case d.RegexpCount > MaxListRegexps:
		return domain.DestList{}, &TooLargeError{Kind: "regexps", Used: d.RegexpCount, Limit: MaxListRegexps}
	}
	now := s.now().UTC()
	return domain.DestList{
		Name: geositeListName(category, norm), Kind: domain.DestListGeosite, Side: domain.DestSideDest,
		GeositeCategory: category, GeositeAttrs: strings.Join(norm, ","),
		Entries: d.Entries, EntryCount: d.EntryCount, RegexpCount: d.RegexpCount, ContentSHA256: d.SHA256, LastFetchedAt: &now,
	}, nil
}

// CreateGeosite stores the list of a category and attribute filter, or returns
// the identical one that already exists.
func (s *Service) CreateGeosite(ctx context.Context, category string, attrs []string) (*domain.DestList, error) {
	unlock, err := s.LockGeosite(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	spec, err := s.GeositeListSpec(ctx, category, attrs)
	if err != nil {
		return nil, err
	}
	if existing, err := s.lists.FindGeosite(ctx, spec.GeositeCategory, spec.GeositeAttrs); err == nil {
		return existing, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if err := s.lists.Create(ctx, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// LockGeosite excludes geosite update and rollback until the returned function
// is called. A caller that derives a list from the current data and then
// stores it (CreateGeosite, a catalog instantiation) holds it across both, so
// an update cannot swap the data — and re-derive the lists — between the two.
// An update that finds it held answers ErrGeositeBusy, and so does a caller
// that finds an update running: nobody queues behind a download.
func (s *Service) LockGeosite(ctx context.Context) (unlock func(), err error) {
	if !s.geo.updateMu.TryLock() {
		return nil, ErrGeositeBusy
	}
	// A leftover swap from an earlier process is settled before anyone derives
	// from the files; this holder owns them now.
	if err := s.healLocked(ctx); err != nil {
		s.geo.updateMu.Unlock()
		return nil, err
	}
	return s.geo.updateMu.Unlock, nil
}

// EnsureGeositeData downloads the latest release when there is none, so the
// first template an admin adds does not fail for want of data. An explicit
// admin action, never a background one.
func (s *Service) EnsureGeositeData(ctx context.Context) error {
	if d, _ := s.currentGeo(ctx); d != nil {
		return nil
	}
	_, err := s.GeositeUpdate(ctx, UpdateOptions{})
	return err
}

// ---- update ----

// UpdateOptions selects what GeositeUpdate does.
type UpdateOptions struct {
	// Tag pins a release; "" is the newest, found by a fresh probe. The admin's
	// dry run shows a diff for one tag and the apply names the same one, so a
	// release that lands in between cannot change what was reviewed.
	Tag string
	// DryRun computes the diff and the verdict and changes nothing.
	DryRun bool
}

// CategoryDiff is one category's change between two releases.
type CategoryDiff struct {
	Name    string `json:"name"`
	Old     int    `json:"old"`
	New     int    `json:"new"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	// State is "added", "removed" or "changed".
	State string `json:"state"`
	// InUse says a policy that is not off uses this category, so the change
	// reaches nodes.
	InUse bool `json:"in_use"`
}

// UpdateRejection is why an update (or rollback) cannot be applied: it would
// make a list in force broad, too large, or empty, or push the policy over a
// limit. The previous data stays.
type UpdateRejection struct {
	// Reason is "broad_entry", "dest_list_too_large", "category_gone" or
	// "policy_over_limit".
	Reason string `json:"reason"`
	List   string `json:"list,omitempty"`
	ListID int64  `json:"list_id,omitempty"`
	Entry  string `json:"entry,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Used   int    `json:"used,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (r *UpdateRejection) Error() string {
	return fmt.Sprintf("geosite update rejected: %s (list %q)", r.Reason, r.List)
}

// ErrGeositeBusy is returned when a geosite update or rollback is running (or
// a list is being derived under the geosite lock): HTTP 409. Callers get it
// at once instead of queueing behind a download.
var ErrGeositeBusy = fmt.Errorf("%w: a geosite update is in progress", domain.ErrConflict)

// ErrGeositeUpdateRejected classifies a refused update (HTTP 409).
var ErrGeositeUpdateRejected = fmt.Errorf("%w: geosite update rejected", domain.ErrConflict)

// UpdateRejectedError carries the rejection.
type UpdateRejectedError struct{ *UpdateRejection }

func (e *UpdateRejectedError) Unwrap() error { return ErrGeositeUpdateRejected }

// GeositeUpdateResult is the outcome of an update or rollback, or its preview.
type GeositeUpdateResult struct {
	DryRun bool         `json:"dry_run"`
	From   *GeositeMeta `json:"from,omitempty"`
	To     GeositeMeta  `json:"to"`
	// Unchanged: the target is the release already in use; nothing was done.
	Unchanged  bool           `json:"unchanged"`
	Categories []CategoryDiff `json:"categories"`
	// Rejection is set on a dry run whose apply would be refused.
	Rejection *UpdateRejection `json:"rejection,omitempty"`
	Applied   bool             `json:"applied"`
	// ListsChanged is how many geosite lists' entries changed; Bumped whether
	// the policy generation moved (only when a policy in force uses a change).
	ListsChanged int  `json:"lists_changed"`
	Bumped       bool `json:"bumped"`
}

// evaluation is what applying one data set would do, before the policies in
// force are known: the lists re-derived, and which of them cannot take the new
// data. decide turns that into a verdict for a given set of policies — it runs
// once on a read taken beforehand (the preview, and an early refusal) and again
// INSIDE the write, on the policies read in the transaction.
type evaluation struct {
	cur, target *dlcData
	byID        map[int64]*domain.DestList
	contents    []domain.DestListContentUpdate
	problems    map[int64]*UpdateRejection
	newEntries  map[int64][]byte
}

// verdict is decide's answer.
type verdict struct {
	rejection *UpdateRejection
	notes     map[int64]string
	forceBump bool
	used      map[string]bool
}

// GeositeUpdate downloads a release (the newest, or opts.Tag), verifies it,
// and — unless DryRun — applies it: every geosite list is re-derived and
// written in one transaction, the generation moves only if a change reaches a
// policy in force, and the previous release is kept for rollback. If applying
// would make a list in force broad, empty or too large, or push the policy
// over a limit, the update is REFUSED and the old data stays.
func (s *Service) GeositeUpdate(ctx context.Context, opts UpdateOptions) (*GeositeUpdateResult, error) {
	g := &s.geo
	if !g.updateMu.TryLock() {
		return nil, ErrGeositeBusy
	}
	defer g.updateMu.Unlock()
	if err := s.healLocked(ctx); err != nil {
		return nil, err
	}

	tag := opts.Tag
	if tag == "" {
		latest, err := s.CheckLatest(ctx, true)
		if err != nil {
			return nil, fmt.Errorf("find the latest geosite release: %w", err)
		}
		tag = latest.Tag
	}
	cur, curMeta := g.current()
	if curMeta != nil && curMeta.Tag == tag {
		return &GeositeUpdateResult{DryRun: opts.DryRun, From: curMeta, To: *curMeta, Unchanged: true, Categories: []CategoryDiff{}}, nil
	}
	raw, sum, err := g.download(ctx, tag)
	if err != nil {
		s.recordUpdateError(err.Error())
		s.observe(RefreshFailed)
		return nil, err
	}
	target, err := parseDLC(raw)
	if err != nil {
		s.recordUpdateError(err.Error())
		s.observe(RefreshFailed)
		return nil, err
	}
	to := GeositeMeta{Tag: tag, SHA256: sum, DownloadedAt: g.now().UTC(), Bytes: int64(len(raw))}
	return s.applyData(ctx, cur, curMeta, target, to, raw, opts.DryRun, false)
}

// GeositeRollback goes back to the previous release. It is applied exactly as
// an update would be — the same checks, one transaction, a generation move
// only for what a policy uses — and the release it leaves becomes the new
// "previous", so a second rollback undoes the first.
func (s *Service) GeositeRollback(ctx context.Context, dryRun bool) (*GeositeUpdateResult, error) {
	g := &s.geo
	if !g.updateMu.TryLock() {
		return nil, ErrGeositeBusy
	}
	defer g.updateMu.Unlock()
	if err := s.healLocked(ctx); err != nil {
		return nil, err
	}

	cur, curMeta := g.current()
	g.mu.Lock()
	g.loadLocked()
	var prev *GeositeMeta
	if g.st.Previous != nil {
		p := *g.st.Previous
		prev = &p
	}
	g.mu.Unlock()
	if prev == nil {
		return nil, fmt.Errorf("%w: there is no previous geosite release to go back to", domain.ErrNotFound)
	}
	raw, err := os.ReadFile(g.path(prevFile))
	if err != nil {
		return nil, fmt.Errorf("%w: the previous geosite release is not on disk", domain.ErrNotFound)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != prev.SHA256 {
		return nil, fmt.Errorf("%w: the previous geosite release no longer matches its record", domain.ErrConflict)
	}
	target, err := parseDLC(raw)
	if err != nil {
		return nil, err
	}
	return s.applyData(ctx, cur, curMeta, target, *prev, raw, dryRun, true)
}

func (s *Service) recordUpdateError(msg string) {
	g := &s.geo
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadLocked()
	g.st.LastUpdateError, g.st.LastUpdateErrorAt = msg, g.now().UTC()
	_ = g.saveLocked()
}

// applyData evaluates target against the lists and policies and, unless dryRun,
// lands it.
//
// THE ORDER IS: stage the file, swap the files in (the old current is set
// aside), commit the lists, and only then let go of the old file; a failed
// commit puts the files back. The other way round — commit, then rename — can
// leave lists derived from data the disk does not have. The commit decides
// inside its own transaction (evaluation.decide on the policies read there), so
// a policy turned on after the preview is still judged.
func (s *Service) applyData(ctx context.Context, cur *dlcData, curMeta *GeositeMeta, target *dlcData, to GeositeMeta, raw []byte, dryRun, rollback bool) (*GeositeUpdateResult, error) {
	ev, err := s.evaluate(ctx, cur, target)
	if err != nil {
		return nil, err
	}
	// A read taken now: the preview's verdict and the diff's in-use flags.
	policies, err := s.policies.List(ctx)
	if err != nil {
		return nil, err
	}
	inUse, inForce := inForceOf(policies)
	pre := ev.decide(inUse, inForce)
	res := &GeositeUpdateResult{DryRun: dryRun, From: curMeta, To: to, Categories: ev.diffs(pre.used), Rejection: pre.rejection}
	if dryRun {
		return res, nil
	}
	if pre.rejection != nil {
		s.refused(pre.rejection)
		res.Rejection = nil
		return res, &UpdateRejectedError{pre.rejection}
	}

	g := &s.geo
	var src string
	if rollback {
		src = g.path(prevFile)
	} else {
		// Stage before touching anything, so a full disk fails the update while
		// nothing has changed.
		if err := os.MkdirAll(g.dir, 0o755); err != nil {
			s.observe(RefreshFailed)
			return nil, fmt.Errorf("create geosite dir: %w", err)
		}
		src = g.path(currentFile + ".new")
		if err := writeFileDurable(src, raw); err != nil {
			_ = os.Remove(src)
			s.observe(RefreshFailed)
			return nil, fmt.Errorf("stage geosite data: %w", err)
		}
	}
	// The pending record is durable BEFORE any file moves: a crash anywhere
	// after this is healed from it (Service.heal), not guessed from the files,
	// and a crash between the record and the first move finds nothing to undo.
	op := "update"
	if rollback {
		op = "rollback"
	}
	if err := g.setPending(&geositePending{Op: op, To: to, From: curMeta}); err != nil {
		if !rollback {
			_ = os.Remove(src)
		}
		s.observe(RefreshFailed)
		return nil, fmt.Errorf("record the geosite swap: %w", err)
	}
	swap, err := g.beginSwap(src)
	if err != nil {
		if !rollback {
			_ = os.Remove(src)
		}
		_ = g.setPending(nil)
		s.observe(RefreshFailed)
		return nil, fmt.Errorf("swap geosite files: %w", err)
	}
	if s.afterSwap != nil {
		s.afterSwap()
	}
	cleanup := func() {
		swap.abort()
		if !rollback {
			_ = os.Remove(src)
		}
		_ = g.setPending(nil)
	}

	var final verdict
	changed, bumped, err := s.lists.ApplyBatch(ctx, domain.DestListBatch{
		Contents: ev.contents,
		Release:  to.Tag,
		Decide: func(inUse map[int64]bool, inForce []*domain.DestPolicy) (map[int64]string, bool, error) {
			final = ev.decide(inUse, inForce)
			if final.rejection != nil {
				return nil, false, &UpdateRejectedError{final.rejection}
			}
			return final.notes, final.forceBump, nil
		},
	})
	if err != nil {
		var rej *UpdateRejectedError
		if errors.As(err, &rej) {
			// The guard refused inside the transaction: nothing committed.
			cleanup()
			s.refused(rej.UpdateRejection)
			return res, err
		}
		// Anything else may have failed AFTER the commit (a lost connection
		// on COMMIT): ask the database which release the lists reflect
		// instead of assuming failure.
		committed, known := s.committedRelease(ctx, to.Tag)
		switch {
		case !known:
			// Leave the swap and the pending record for heal: the files are
			// in the state the record describes.
			s.observe(RefreshFailed)
			return nil, fmt.Errorf("the outcome of the geosite update is unknown; it will be settled by a later call: %w", err)
		case !committed:
			cleanup()
			s.observe(RefreshFailed)
			return nil, err
		}
		log.Warn("destlist: the geosite commit reported an error but the lists carry the new release; completing", "err", err)
		changed, bumped = nil, false
	}
	res.Applied, res.ListsChanged, res.Bumped = true, len(changed), bumped
	res.Categories = ev.diffs(final.used)

	g.mu.Lock()
	g.loadLocked()
	if err := g.commitLocked(swap, to, target); err != nil {
		// The lists are committed and the files are in place; only the record
		// failed to save. The pending marker (if it did not clear) lets the
		// next start finish the job.
		log.Error("destlist: geosite release applied but its record could not be saved", "err", err)
	}
	g.mu.Unlock()
	if len(changed) > 0 || bumped {
		s.observe(RefreshUpdated)
	} else {
		s.observe(RefreshUnchanged)
	}
	return res, nil
}

// committedRelease asks the database whether the geosite lists now reflect
// release tag. known is false when it cannot be asked.
func (s *Service) committedRelease(ctx context.Context, tag string) (committed, known bool) {
	if s.state == nil {
		return false, false
	}
	st, err := s.state.Get(ctx)
	if err != nil {
		return false, false
	}
	return st.GeositeRelease == tag, true
}

// setPending records (or clears, with nil) the swap in flight and fsyncs it.
func (g *geositeStore) setPending(p *geositePending) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadLocked()
	g.st.Pending = p
	return g.saveLocked()
}

// refused records an update the guard refused.
func (s *Service) refused(r *UpdateRejection) {
	s.recordUpdateError(r.Error())
	if r.Reason == "broad_entry" {
		s.observe(RefreshBroad)
	} else {
		s.observe(RefreshFailed)
	}
}

// swapState is a file swap in progress: the new release is in place and the old
// one is set aside until the lists have committed.
type swapState struct {
	g      *geositeStore
	src    string
	hadOld bool
}

// beginSwap puts the release at src in place as the current file, setting the
// existing one aside. Held by the geosite lock, so nothing else touches the
// files meanwhile.
func (g *geositeStore) beginSwap(src string) (*swapState, error) {
	cur, aside := g.path(currentFile), g.path(currentFile+rollbackSuffix)
	st := &swapState{g: g, src: src}
	if _, err := os.Stat(cur); err == nil {
		if err := os.Rename(cur, aside); err != nil {
			return nil, err
		}
		st.hadOld = true
	}
	if err := os.Rename(src, cur); err != nil {
		if st.hadOld {
			_ = os.Rename(aside, cur)
		}
		return nil, err
	}
	return st, nil
}

// abort puts the files back as they were.
func (st *swapState) abort() {
	cur, aside := st.g.path(currentFile), st.g.path(currentFile+rollbackSuffix)
	_ = os.Rename(cur, st.src)
	if st.hadOld {
		_ = os.Rename(aside, cur)
	}
	_ = syncDir(st.g.dir)
}

// commitLocked finishes a swap after the lists committed: the old current
// becomes the previous release and the record is saved.
func (g *geositeStore) commitLocked(st *swapState, to GeositeMeta, target *dlcData) error {
	if st.hadOld {
		if err := os.Rename(g.path(currentFile+rollbackSuffix), g.path(prevFile)); err != nil {
			return err
		}
	}
	g.st.Previous = g.st.Current
	t := to
	g.st.Current = &t
	g.data, g.dataErr = target, ""
	g.st.LastUpdateError, g.st.LastUpdateErrorAt = "", time.Time{}
	g.st.Pending = nil
	if err := syncDir(g.dir); err != nil {
		return err
	}
	return g.saveLocked()
}

// evaluate re-derives every geosite list from target and notes which cannot
// take it.
func (s *Service) evaluate(ctx context.Context, cur, target *dlcData) (*evaluation, error) {
	lists, err := s.lists.List(ctx, true)
	if err != nil {
		return nil, err
	}
	ev := &evaluation{
		cur: cur, target: target, byID: make(map[int64]*domain.DestList, len(lists)),
		problems: map[int64]*UpdateRejection{}, newEntries: map[int64][]byte{},
	}
	now := s.now()
	for _, l := range lists {
		ev.byID[l.ID] = l
		if l.Kind != domain.DestListGeosite {
			ev.newEntries[l.ID] = l.Entries
			continue
		}
		d, ok := target.derive(l.GeositeCategory, splitAttrs(l.GeositeAttrs))
		problem := ""
		switch {
		case !ok || d.EntryCount == 0:
			problem = "category_gone"
		case d.Broad != "":
			problem = "broad_entry"
		case d.EntryCount > MaxListEntries || d.RegexpCount > MaxListRegexps:
			problem = "dest_list_too_large"
		}
		if problem != "" {
			r := &UpdateRejection{Reason: problem, List: l.Name, ListID: l.ID}
			if d != nil {
				r.Entry = d.Broad
			}
			ev.problems[l.ID] = r
			ev.newEntries[l.ID] = l.Entries
			continue
		}
		ev.newEntries[l.ID] = d.Entries
		ev.contents = append(ev.contents, domain.DestListContentUpdate{ListID: l.ID, DestListContent: domain.DestListContent{
			Entries: d.Entries, EntryCount: d.EntryCount, RegexpCount: d.RegexpCount, ContentSHA256: d.SHA256, FetchedAt: now.UTC(),
		}})
	}
	return ev, nil
}

// inForceOf splits policies into those that are not off and the lists they use.
func inForceOf(policies []*domain.DestPolicy) (map[int64]bool, []*domain.DestPolicy) {
	inUse := map[int64]bool{}
	var inForce []*domain.DestPolicy
	for _, p := range policies {
		if p.State == domain.DestStateOff {
			continue
		}
		inForce = append(inForce, p)
		for _, id := range p.ListIDs {
			inUse[id] = true
		}
	}
	return inUse, inForce
}

// decide judges applying the evaluated data beside these policies: a list in
// force that cannot take the data refuses the update, one that is not in force
// just records why, a custom list referencing a category whose entries changed
// forces a republish, and the policies in force must still fit the wire's caps.
func (ev *evaluation) decide(inUse map[int64]bool, inForce []*domain.DestPolicy) verdict {
	v := verdict{notes: map[int64]string{}, used: map[string]bool{}}
	ids := make([]int64, 0, len(ev.problems))
	for id := range ev.problems {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if inUse[id] {
			if v.rejection == nil {
				v.rejection = ev.problems[id]
			}
		} else {
			v.notes[id] = ev.problems[id].Reason
		}
	}
	for id, l := range ev.byID {
		if !inUse[id] {
			continue
		}
		if l.Kind == domain.DestListGeosite {
			v.used[l.GeositeCategory] = true
			continue
		}
		for _, ref := range geositeRefs(l.Entries) {
			v.used[ref.name] = true
			if ev.cur == nil {
				v.forceBump = true
				continue
			}
			before, _ := ev.cur.derive(ref.name, ref.attrs)
			after, _ := ev.target.derive(ref.name, ref.attrs)
			if before == nil || after == nil || before.SHA256 != after.SHA256 {
				v.forceBump = true
			}
		}
	}
	if v.rejection == nil {
		v.rejection = overLimit(inForce, ev.byID, ev.newEntries, ev.target)
	}
	if v.rejection != nil {
		v.forceBump = false
	}
	return v
}

// diffs is the per-category summary between the current and the target data.
func (ev *evaluation) diffs(used map[string]bool) []CategoryDiff {
	out := []CategoryDiff{}
	for _, d := range diffCategories(ev.cur, ev.target) {
		out = append(out, CategoryDiff{Name: d.Name, Old: d.Old, New: d.New, Added: d.Added, Removed: d.Removed, State: d.State, InUse: used[d.Name]})
	}
	return out
}

func splitAttrs(csv string) []string {
	if csv == "" {
		return nil
	}
	return strings.Split(csv, ",")
}

type geositeRef struct {
	name  string
	attrs []string
}

func geositeRefs(entries []byte) []geositeRef {
	var out []geositeRef
	for _, e := range strings.Split(string(entries), "\n") {
		if strings.HasPrefix(e, "geosite:") {
			if name, attrs, ok := splitGeosite(strings.TrimPrefix(e, "geosite:")); ok {
				out = append(out, geositeRef{name: name, attrs: attrs})
			}
		}
	}
	return out
}

// overLimit sums what the policies in force would send after the update and
// compares it with the wire policy's caps. The sum is per policy, the way the
// wire counts (one list in two policies is two sets of rules), and covers the
// destination domains, regexps and CIDRs; GeoIP countries and the per-node
// subjects are the publisher's to count exactly. This is the guard that keeps
// an update from being applied only to fail every publish after it.
func overLimit(inForce []*domain.DestPolicy, byID map[int64]*domain.DestList, entries map[int64][]byte, target *dlcData) *UpdateRejection {
	var domains, regexps, cidrs int
	for _, p := range inForce {
		cidrs += len(p.Inline.CIDRs)
		for _, id := range p.ListIDs {
			l := byID[id]
			if l == nil || l.Side == domain.DestSideSource {
				continue
			}
			d, r, c := countEntryKinds(entries[id], target)
			domains, regexps, cidrs = domains+d, regexps+r, cidrs+c
		}
	}
	for _, c := range []struct {
		kind        string
		used, limit int
	}{
		{"domains", domains, protocol.MaxPolicyDomains},
		{"regexps", regexps, protocol.MaxPolicyRegexps},
		{"cidrs", cidrs, protocol.MaxPolicyCIDRs},
	} {
		if c.used > c.limit {
			return &UpdateRejection{Reason: "policy_over_limit", Kind: c.kind, Used: c.used, Limit: c.limit}
		}
	}
	return nil
}

// countEntryKinds counts a list's entries by what they cost against the
// policy's caps, expanding geosite references from target.
func countEntryKinds(entries []byte, target *dlcData) (domains, regexps, cidrs int) {
	for _, e := range strings.Split(string(entries), "\n") {
		switch {
		case e == "":
		case strings.HasPrefix(e, "regexp:"):
			regexps++
		case strings.HasPrefix(e, "domain:"), strings.HasPrefix(e, "full:"), strings.HasPrefix(e, "keyword:"):
			domains++
		case strings.HasPrefix(e, "geosite:"):
			if name, attrs, ok := splitGeosite(strings.TrimPrefix(e, "geosite:")); ok {
				if d, ok := target.derive(name, attrs); ok {
					domains += d.EntryCount - d.RegexpCount
					regexps += d.RegexpCount
				}
			}
		case strings.HasPrefix(e, "geoip:"):
		default:
			cidrs++
		}
	}
	return domains, regexps, cidrs
}
