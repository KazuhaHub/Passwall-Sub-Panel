package destpolicy

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"golang.org/x/net/publicsuffix"
)

type ExceptionStore interface {
	ReadDefinitions(context.Context) (domain.DestDefinitions, error)
	GetGroupMode(context.Context, int64) (domain.DestGroupMode, error)
	AddGlobalException(context.Context, time.Time, func(domain.DestList) (domain.DestList, error)) (domain.DestGlobalExceptionCommit, error)
	AddGroupException(context.Context, int64, time.Time, func(domain.DestList) (domain.DestList, error)) (int64, error)
}
type ExceptionResult struct {
	Commit domain.DestGlobalExceptionCommit
	Entry  string
}
type ExceptionManager struct {
	store ExceptionStore
	gate  *operationgate.Gate
	now   func() time.Time
}

func NewExceptionManager(store ExceptionStore) *ExceptionManager {
	return &ExceptionManager{store: store, now: time.Now}
}
func (m *ExceptionManager) SetOperationGate(gate *operationgate.Gate) { m.gate = gate }

// Normalize targets locally; URLs contribute only their hostname. IP exceptions
// always denote one address. Neither path performs DNS resolution.
func exceptionEntry(target, match string) (string, error) {
	if match != "site" && match != "host" {
		return "", invalid("match")
	}
	target = strings.TrimSpace(target)
	if target == "" || len(target) > 4096 || strings.ContainsAny(target, "\r\n\t") {
		return "", invalid("target")
	}
	host := target
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return "", invalid("target")
		}
		ip = ip.Unmap()
		return netip.PrefixFrom(ip, ip.BitLen()).String(), nil
	}
	parsedTarget := target
	if !strings.Contains(target, "://") {
		parsedTarget = "//" + target
	}
	u, err := url.Parse(parsedTarget)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return "", invalid("target")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p <= 0 || p > 65535 {
			return "", invalid("target")
		}
	}
	host = u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return "", invalid("target")
		}
		ip = ip.Unmap()
		return netip.PrefixFrom(ip, ip.BitLen()).String(), nil
	}
	parsed, err := destlist.ParseCustom([]byte("full:" + host))
	if err != nil || parsed.EntryCount != 1 || parsed.Report.IgnoredBroad > 0 {
		return "", invalid("target")
	}
	host = strings.TrimPrefix(strings.TrimSpace(string(parsed.Entries)), "full:")
	entry := "full:" + host
	if match == "site" {
		site, err := publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil {
			return "", invalid("target")
		}
		entry = "domain:" + site
	}
	if destlist.IsBroad(entry) {
		return "", invalid("target")
	}
	return entry, nil
}
func exceptionPatch(list domain.DestList, entry string) (domain.DestList, error) {
	if list.Kind != domain.DestListCustom || list.OwnerGroupID != 0 {
		return domain.DestList{}, fmt.Errorf("%w: dest_exception_conflict", domain.ErrConflict)
	}
	for _, old := range strings.Split(string(list.Entries), "\n") {
		if old == entry {
			return list, nil
		}
	}
	return destlist.PrepareEntryPatch(list, true, []string{entry}, nil)
}
func hypotheticalException(defs domain.DestDefinitions, entry string) (domain.DestDefinitions, error) {
	defs.Lists, defs.Policies = slices.Clone(defs.Lists), slices.Clone(defs.Policies)
	marker := -1
	var maxList, maxPolicy int64
	for _, l := range defs.Lists {
		maxList = max(maxList, l.ID)
	}
	for i, p := range defs.Policies {
		maxPolicy = max(maxPolicy, p.ID)
		if p.TemplateKey == domain.DestGlobalExceptionTemplateKey {
			if marker >= 0 {
				return domain.DestDefinitions{}, fmt.Errorf("%w: dest_exception_conflict", domain.ErrConflict)
			}
			marker = i
		}
	}
	if marker >= 0 {
		p := defs.Policies[marker]
		if p.Action != domain.DestAllow || p.Scope != domain.DestScopeAll || !p.Enabled || len(p.ListIDs) != 1 || len(p.GroupIDs) != 0 || len(p.Inline.CIDRs) > 0 || p.Inline.Ports != "" || p.Inline.Network != "" || len(p.Inline.Protocols) > 0 || p.Inline.Private {
			return domain.DestDefinitions{}, fmt.Errorf("%w: dest_exception_conflict", domain.ErrConflict)
		}
		found := false
		for i, list := range defs.Lists {
			if list.ID == p.ListIDs[0] {
				patched, err := exceptionPatch(list, entry)
				if err != nil {
					return domain.DestDefinitions{}, err
				}
				defs.Lists[i] = patched
				found = true
				break
			}
		}
		if !found {
			return domain.DestDefinitions{}, fmt.Errorf("%w: dest_exception_conflict", domain.ErrConflict)
		}
	} else {
		if maxList == math.MaxInt64 || maxPolicy == math.MaxInt64 {
			return domain.DestDefinitions{}, domain.ErrResourceExhausted
		}
		list, err := exceptionPatch(domain.DestList{ID: maxList + 1, Kind: domain.DestListCustom}, entry)
		if err != nil {
			return domain.DestDefinitions{}, err
		}
		defs.Lists = append(defs.Lists, list)
		for i, p := range defs.Policies {
			if p.Action == domain.DestAllow {
				if p.Priority == math.MaxInt {
					return domain.DestDefinitions{}, domain.ErrResourceExhausted
				}
				defs.Policies[i].Priority++
			}
		}
		defs.Policies = append(defs.Policies, domain.DestPolicy{ID: maxPolicy + 1, Action: domain.DestAllow, Scope: domain.DestScopeAll, Enabled: true, Priority: 1, ListIDs: []int64{list.ID}, TemplateKey: domain.DestGlobalExceptionTemplateKey})
	}
	return defs, CheckDefinitions(defs)
}
func (m *ExceptionManager) Global(ctx context.Context, target, match string) (ExceptionResult, error) {
	if m == nil || m.store == nil {
		return ExceptionResult{}, domain.ErrUnavailable
	}
	entry, err := exceptionEntry(target, match)
	if err != nil {
		return ExceptionResult{}, err
	}
	ctx, release, err := m.gate.Read(ctx)
	if err != nil {
		return ExceptionResult{}, err
	}
	defer release()
	defs, err := m.store.ReadDefinitions(ctx)
	if err != nil {
		return ExceptionResult{}, err
	}
	if _, err := hypotheticalException(defs, entry); err != nil {
		return ExceptionResult{}, err
	}
	commit, err := m.store.AddGlobalException(ctx, m.now().UTC(), func(list domain.DestList) (domain.DestList, error) { return exceptionPatch(list, entry) })
	if err != nil {
		return ExceptionResult{}, err
	}
	return ExceptionResult{Commit: commit, Entry: entry}, nil
}
