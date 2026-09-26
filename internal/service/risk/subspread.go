package risk

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// placedWindow is one account's window after address hygiene and placement:
// what domain.EvaluateSubSpread reads.
type placedWindow struct {
	sources, placed, regionKnown int
	excluded                     domain.GeoExcluded
	sightings                    []domain.SubPlaceSighting
	identities                   []domain.SubIdentity
}

// placement is the whole fleet's window placed in one pass, and the rules
// it was placed under: the login check sets addresses aside with the same
// ignore list, parsed (and warned about) once.
type placement struct {
	geoAvailable bool
	ignore       domain.GeoIgnoreList
	users        map[int64]*placedWindow
}

// subSpread judges every account's fetch window (domain.EvaluateSubSpread)
// and appends one sub_spread row per account of a readable group.
func (s *Service) subSpread(ctx context.Context, r *refresh, w *fetchWindow, pl placement) error {
	for _, u := range r.users {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("risk refresh: %w", err)
		}
		policy, ok := r.policies[u.GroupID]
		if !ok {
			continue // unreadable group: previous rows kept
		}
		in := domain.SubSpreadInput{
			WindowDays:    w.days,
			RetentionDays: max(r.global.SubLogRetentionDays, 0),
			WindowStart:   w.start,
			GeoAvailable:  pl.geoAvailable,
		}
		if pw := pl.users[u.ID]; pw != nil {
			in.Fetched = true
			in.Sources, in.Placed, in.RegionKnown = pw.sources, pw.placed, pw.regionKnown
			in.Excluded = pw.excluded
			in.Sightings, in.Identities = pw.sightings, pw.identities
		}
		v, ev := domain.EvaluateSubSpread(domain.SubSpreadPolicy{
			Off: policy.risk.SubSpreadOff, Geo: policy.geo, MinDays: policy.risk.MinDays,
		}, in)
		addVerdict(r, u.ID, domain.RiskKindSubSpread, v, ev)
	}
	return nil
}

// placeWindow applies the concurrent-location check's address hygiene to the
// whole window at once and places what is left with one geo lookup.
//
// The exclusions are v2's, in v2's order — internal ranges, the admin ignore
// list, PSP's own nodes and relays, then shared exits — and one IPv6 /64 is
// one source. "Shared" here means three or more accounts fetched from one
// source within the window, which is why the fleet is classified together:
// an office or a carrier gateway says nothing about where any one account
// is. Only listed accounts are in the window, so a ghost account cannot tip
// a household into an office.
//
// The ignore list's invalid entries are skipped, not fatal. The Warn names
// no entry: the traffic poll logs them every cycle, and this package logs no
// address-shaped text at all.
func (s *Service) placeWindow(ctx context.Context, r *refresh, w *fetchWindow) placement {
	ignore, err := domain.ParseGeoIgnoreList(r.global.GeoAnomalyIgnoreAddresses)
	if err != nil {
		log.Warn("risk signals: the geo ignore list has invalid entries; applying the valid ones")
	}
	live := make(map[int64]domain.UserLiveIPs, len(w.users))
	for uid, uw := range w.users {
		ips := make([]string, 0, len(uw.ips))
		for ip := range uw.ips {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
		// Every fetch in the window is "live" for this purpose: the window
		// is the sample, and nothing here is about being connected now.
		live[uid] = domain.UserLiveIPs{UserID: uid, IPs: ips, Fresh: ips}
	}
	addrs := domain.ClassifyAddresses(live, domain.AddressExclusions{
		Internal:       true,
		Ignore:         ignore,
		Infra:          s.d.IsInfra,
		SharedMinUsers: domain.SharedExitMinUsers,
	})

	pl := placement{
		geoAvailable: s.d.Geo != nil && s.d.Geo.Available(ctx),
		ignore:       ignore,
		users:        make(map[int64]*placedWindow, len(w.users)),
	}
	located := map[string]domain.GeoLocation{}
	if pl.geoAvailable {
		// One batch for the whole fleet: the resolver holds its read lock
		// for a batch, and a week of accounts is one question, not
		// thousands.
		seen := map[string]bool{}
		var ips []string
		for _, a := range addrs {
			for _, k := range a.Kept {
				if !seen[k.LookupIP] {
					seen[k.LookupIP] = true
					ips = append(ips, k.LookupIP)
				}
			}
		}
		sort.Strings(ips)
		if len(ips) > 0 {
			located = s.d.Geo.Lookup(ctx, ips)
		}
	}

	for uid, uw := range w.users {
		a := addrs[uid]
		pw := &placedWindow{excluded: a.Excluded}
		kept := make(map[string]domain.SourceAddr, len(a.Kept))
		for _, k := range a.Kept {
			kept[k.Key] = k
			pw.sources++
			if cc, region := placeOf(located[k.LookupIP]); cc != "" {
				pw.placed++
				if region != "" {
					pw.regionKnown++
				}
			}
		}
		type sightKey struct{ identity, cc, region string }
		days := map[sightKey]uint8{}
		for cell, mask := range uw.cells {
			key, _, _ := domain.SourceKey(cell.ip)
			src, ok := kept[key]
			if !ok {
				continue // excluded: it says nothing about where the account is
			}
			cc, region := placeOf(located[src.LookupIP])
			days[sightKey{cell.identity, cc, region}] |= mask
		}
		for k, mask := range days {
			pw.sightings = append(pw.sightings, domain.SubPlaceSighting{Identity: k.identity, CC: k.cc, Region: k.region, Days: mask})
		}
		sort.Slice(pw.sightings, func(i, j int) bool {
			a, b := pw.sightings[i], pw.sightings[j]
			if a.Identity != b.Identity {
				return a.Identity < b.Identity
			}
			if a.CC != b.CC {
				return a.CC < b.CC
			}
			return a.Region < b.Region
		})
		for key, agg := range uw.identities {
			pw.identities = append(pw.identities, domain.SubIdentity{Key: key, Kind: agg.kind, Label: agg.label, HWID4: agg.hwid4})
		}
		sort.Slice(pw.identities, func(i, j int) bool { return pw.identities[i].Key < pw.identities[j].Key })
		pl.users[uid] = pw
	}
	return pl
}

// placeOf is where a lookup put a source: the country code upper-cased (a
// database answering "jp" is not a second country beside "JP") and the
// region trimmed. No country means no region — "Springfield" is not a place
// until the country is known.
func placeOf(g domain.GeoLocation) (cc, region string) {
	cc = strings.ToUpper(strings.TrimSpace(g.CountryCode))
	if cc == "" {
		return "", ""
	}
	return cc, strings.TrimSpace(g.Region)
}
