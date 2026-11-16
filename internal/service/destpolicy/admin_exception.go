package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// ExceptionResult is what adding a global exception did.
type ExceptionResult struct {
	// Entry is the list line that was added.
	Entry string `json:"entry"`
	// ListID and PolicyID are the exception list and the allow policy over it.
	ListID   int64 `json:"list_id"`
	PolicyID int64 `json:"policy_id"`
	// Created says this call made the list and the policy (the first one).
	Created bool `json:"created"`
}

// AddException allows one destination everywhere: it appends the site (a domain
// and everything under it) or the host (exactly that name) to the exceptions
// list, which an allow policy at the head of the allows turns into a pass for
// everybody, ahead of every block. The first call creates the list and the
// policy in one transaction; later calls only append.
//
// "Allow for one account only" is an exemption, not this.
func (a *Admin) AddException(ctx context.Context, target, match string) (*ExceptionResult, error) {
	line, err := ExceptionLine(target, match)
	if err != nil {
		return nil, err
	}
	if a.Instantiate == nil || a.ExceptionEditor == nil {
		return nil, fmt.Errorf("%w: exceptions are not available in this build", domain.ErrUnavailable)
	}

	policies, err := a.Policies.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range policies {
		if p.TemplateKey == exceptionTemplateKey && len(p.ListIDs) == 1 {
			if _, _, err := a.ExceptionEditor.AddEntries(ctx, p.ListIDs[0], []string{line}, nil); err != nil {
				return nil, err
			}
			return &ExceptionResult{Entry: line, ListID: p.ListIDs[0], PolicyID: p.ID}, nil
		}
	}

	parsed, err := destlist.ParseCustom([]byte(line), domain.DestSideDest)
	if err != nil {
		return nil, err
	}
	now := a.Now().UTC()
	list := domain.DestList{
		Name: exceptionListName, Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: parsed.Entries,
		SourceText: []byte(line + "\n"), EntryCount: parsed.EntryCount, RegexpCount: parsed.RegexpCount,
		ContentSHA256: parsed.SHA256, LastFetchedAt: &now,
	}
	policy := &domain.DestPolicy{
		Name: exceptionPolicyName, Description: exceptionPolicyDescription, Action: domain.DestActionAllow,
		State: domain.DestStateOn, Scope: domain.DestScopeAll, TemplateKey: exceptionTemplateKey,
	}
	created, err := a.Instantiate(ctx, []domain.DestList{list}, policy)
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			// Another request created it a moment ago: append to theirs.
			return a.AddException(ctx, target, match)
		}
		return nil, err
	}
	// First among the allows: an exception must beat every other allow's
	// narrower reading of the same host. (A failure leaves it last, which is
	// still an exception that works.)
	var ids []int64
	ids = append(ids, created.ID)
	rest := slices.DeleteFunc(slices.Clone(policies), func(p *domain.DestPolicy) bool { return p.Action != domain.DestActionAllow })
	slices.SortStableFunc(rest, func(x, y *domain.DestPolicy) int { return x.Priority - y.Priority })
	for _, p := range rest {
		ids = append(ids, p.ID)
	}
	_ = a.Policies.Reorder(ctx, domain.DestActionAllow, ids)
	return &ExceptionResult{Entry: line, ListID: created.ListIDs[0], PolicyID: created.ID, Created: true}, nil
}

// ExceptionLine is the list line an exception adds: the site (a domain and
// everything under it) or the host (exactly that name) of what the admin typed.
// A *ValidationError (CodeExceptionInvalid) says which field was not understood.
func ExceptionLine(target, match string) (string, error) {
	host, err := exceptionHost(target)
	if err != nil {
		return "", err
	}
	switch match {
	case "site":
		return "DOMAIN-SUFFIX," + host, nil
	case "host":
		return "DOMAIN," + host, nil
	}
	return "", &ValidationError{Code: CodeExceptionInvalid, Field: "match", Bad: []string{match}, Msg: "match is site or host"}
}

// exceptionHost reads a host name out of what the admin typed: a bare name, a
// name with a port, or a URL.
func exceptionHost(target string) (string, error) {
	t := strings.TrimSpace(target)
	if strings.Contains(t, "://") {
		u, err := url.Parse(t)
		if err != nil || u.Hostname() == "" {
			return "", &ValidationError{Code: CodeExceptionInvalid, Field: "target", Bad: []string{target}, Msg: "not a URL with a host"}
		}
		t = u.Hostname()
	} else if i := strings.IndexAny(t, "/?#"); i >= 0 {
		t = t[:i]
	}
	if h, _, ok := strings.Cut(t, ":"); ok && !strings.Contains(h, ":") && strings.Count(t, ":") == 1 {
		t = h
	}
	host, ok := destlist.NormalizeDomain(t)
	if !ok || len(host) > maxExceptionTargetBytes {
		return "", &ValidationError{Code: CodeExceptionInvalid, Field: "target", Bad: []string{target}, Msg: "not a host name"}
	}
	return host, nil
}
