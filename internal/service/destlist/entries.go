package destlist

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// AddEntries adds lines to and removes lines from a custom list's text in one
// write: the generation advances once, and nothing the admin typed besides the
// lines concerned (comments, order, spelling of the other lines) is touched.
// The "add to exceptions" and "add to the allowlist" buttons are this call, so
// the page never has to rebuild a list's whole text.
//
// add lines are in the list grammar and are appended as given. A remove entry
// matches a line by what it MEANS, not how it is spelled: removing "example.com"
// takes out "DOMAIN-SUFFIX,Example.COM" too. The edit is read-modify-write on
// the stored text under a lock, so two admins pressing the button at once both
// land.
func (s *Service) AddEntries(ctx context.Context, id int64, add, remove []string) (*domain.DestList, *ParseReport, error) {
	if len(add)+len(remove) == 0 {
		return nil, nil, fmt.Errorf("%w: dest list: nothing to add or remove", domain.ErrValidation)
	}
	s.entriesMu.Lock()
	defer s.entriesMu.Unlock()

	l, err := s.lists.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if l.Kind != domain.DestListCustom {
		return nil, nil, fmt.Errorf("%w: only a custom list has text to edit", domain.ErrValidation)
	}
	drop := map[string]bool{}
	for _, r := range remove {
		canon, err := canonicalOf(r, l.Side)
		if err != nil {
			return nil, nil, err
		}
		if canon != "" {
			drop[canon] = true
		}
	}
	var out []string
	if text := strings.TrimRight(string(l.SourceText), "\r\n"); text != "" {
		for _, line := range strings.Split(text, "\n") {
			if len(drop) > 0 {
				if canon, err := canonicalOf(line, l.Side); err == nil && canon != "" && drop[canon] {
					continue
				}
			}
			out = append(out, line)
		}
	}
	for _, a := range add {
		a = strings.TrimSpace(a)
		if a == "" || strings.ContainsAny(a, "\r\n") {
			return nil, nil, fmt.Errorf("%w: dest list: an entry is one non-empty line", domain.ErrValidation)
		}
		out = append(out, a)
	}
	text := []byte(strings.Join(out, "\n") + "\n")
	p, err := ParseCustom(text, l.Side)
	if err != nil {
		return nil, nil, err
	}
	now := s.now().UTC()
	l.SourceText, l.Entries = text, p.Entries
	l.EntryCount, l.RegexpCount, l.ContentSHA256, l.LastFetchedAt, l.LastError = p.EntryCount, p.RegexpCount, p.SHA256, &now, ""
	if err := s.lists.Update(ctx, l); err != nil {
		return nil, nil, err
	}
	return l, &p.Report, nil
}

// canonicalOf is the canonical entry a single line parses to ("" when it is a
// comment or yields none), so lines can be compared by meaning.
func canonicalOf(line string, side domain.DestListSide) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil
	}
	p, err := ParseCustom([]byte(line), side)
	if err != nil {
		return "", err
	}
	return string(p.Entries), nil
}

// RemotePreview is what fetching a remote list's URL would store, without
// storing it.
type RemotePreview struct {
	Parsed     *Parsed
	HTTPStatus int
	Bytes      int
}

// PreviewRemote fetches rawURL exactly as a real list would (https only, the
// SSRF-refusing client, the same size cap) and reports what it parsed to. It
// writes nothing.
func (s *Service) PreviewRemote(ctx context.Context, rawURL string, side domain.DestListSide) (*RemotePreview, error) {
	if side == "" {
		side = domain.DestSideDest
	}
	u, err := ValidateRemoteURL(rawURL)
	if err != nil {
		return nil, err
	}
	body, err := fetchBody(ctx, s.doer, u.String(), MaxRemoteBytes)
	if err != nil {
		return nil, err
	}
	p, err := ParseRemote(body, side)
	if err != nil {
		return nil, err
	}
	// fetchBody answers only for a 2xx; the status itself is not kept past it.
	return &RemotePreview{Parsed: p, HTTPStatus: 200, Bytes: len(body)}, nil
}

// UpdateRemote renames a remote list and, when the URL changed, fetches the new
// one before keeping it: a URL that does not answer changes nothing.
func (s *Service) UpdateRemote(ctx context.Context, id int64, name, rawURL string) (*domain.DestList, *ParseReport, error) {
	if err := checkName(name); err != nil {
		return nil, nil, err
	}
	l, err := s.lists.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if l.Kind != domain.DestListRemote {
		return nil, nil, fmt.Errorf("%w: only a remote list has a URL", domain.ErrValidation)
	}
	u, err := ValidateRemoteURL(rawURL)
	if err != nil {
		return nil, nil, err
	}
	l.Name = name
	if u.String() == l.SourceURL {
		if err := s.lists.Update(ctx, l); err != nil {
			return nil, nil, err
		}
		return l, nil, nil
	}
	p, err := s.fetchParsed(ctx, u.String(), l.Side)
	if err != nil {
		return nil, nil, err
	}
	now := s.now().UTC()
	l.SourceURL, l.Entries = u.String(), p.Entries
	l.EntryCount, l.RegexpCount, l.ContentSHA256, l.LastFetchedAt, l.LastError = p.EntryCount, p.RegexpCount, p.SHA256, &now, ""
	if err := s.lists.Update(ctx, l); err != nil {
		return nil, nil, err
	}
	return l, &p.Report, nil
}

// PolicyRef names something that uses a list.
type PolicyRef struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// UsedBy maps each list id to the policies that reference it (any state: a
// policy that is off still blocks deleting the list it will use when switched
// on), in policy order.
func (s *Service) UsedBy(ctx context.Context) (map[int64][]PolicyRef, error) {
	policies, err := s.policies.List(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64][]PolicyRef{}
	slices.SortStableFunc(policies, func(a, b *domain.DestPolicy) int { return int(a.ID - b.ID) })
	for _, p := range policies {
		for _, id := range p.ListIDs {
			out[id] = append(out[id], PolicyRef{Kind: "policy", ID: p.ID, Name: p.Name})
		}
	}
	return out, nil
}

// BeginRefresh marks a list as being refreshed and reports whether this caller
// should do the refresh: false when one is already running. The page shows the
// "refreshing" state from it.
func (s *Service) BeginRefresh(id int64) bool {
	s.refreshingMu.Lock()
	defer s.refreshingMu.Unlock()
	if s.refreshing == nil {
		s.refreshing = map[int64]time.Time{}
	}
	if _, running := s.refreshing[id]; running {
		return false
	}
	s.refreshing[id] = s.now()
	return true
}

// EndRefresh clears the mark BeginRefresh set.
func (s *Service) EndRefresh(id int64) {
	s.refreshingMu.Lock()
	defer s.refreshingMu.Unlock()
	delete(s.refreshing, id)
}

// Refreshing reports whether a refresh of the list is running.
func (s *Service) Refreshing(id int64) bool {
	s.refreshingMu.Lock()
	defer s.refreshingMu.Unlock()
	_, running := s.refreshing[id]
	return running
}
