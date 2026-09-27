package risk

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// authPageSize is how many auth events one List call asks for. It must not
// exceed the repository's cap (applyPagination serves at most 200 rows a
// page): the loop stops on a short page, so a larger ask would be served 200
// rows, read them as the last page, and silently leave every later login
// unread.
const authPageSize = 200

// accountLogin is one successful login as the run holds it: when, how, and
// from where. Nothing else of the event is kept, and the address does not
// outlive placeLogins — the sightings the evaluator reads carry a country
// code.
type accountLogin struct {
	atMS   int64
	method string
	ip     string
}

// loginLookbackDays is how far back the login log is read: the configured
// risk.login_lookback_days (90 by default), or the auth-event retention when
// that is shorter — the rows before it are gone, and a lookback reaching
// past them would read those days as days without logins. 0 (keep forever)
// leaves the configured lookback. As with the fetch window, the retention
// applies only here, where the log is read; a group's hold is bounded by
// the configured value.
func loginLookbackDays(retention, configured int) int {
	if retention > 0 && retention < configured {
		return retention
	}
	return configured
}

// loginCountry judges every account's successful panel logins
// (domain.EvaluateLoginCountry) against the countries its fetches
// established, and appends one login_country row per account of a readable
// group.
//
// The caller runs it only on a fetch window read in full and placed against
// a loaded infrastructure set: the window's countries are the known ones,
// and a login is placed with the same address rules. A login log that
// cannot be read costs this kind its rows for the run — the stored ones
// stay — and the run is partial.
func (s *Service) loginCountry(ctx context.Context, r *refresh, pl placement) error {
	lookback := loginLookbackDays(r.global.AuthEventRetentionDays, r.rt.LoginLookbackDays)
	logins, err := s.readLogins(ctx, r, r.now.Add(-time.Duration(lookback)*24*time.Hour))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("risk refresh: %w", ctx.Err())
		}
		r.partial = true
		log.Warn("risk signals: the login log is unreadable; login_country keeps its previous rows", "err", err)
		return nil
	}
	sightings := s.placeLogins(ctx, pl, logins)
	nowMS := r.now.UnixMilli()
	for _, u := range r.users {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("risk refresh: %w", err)
		}
		policy, ok := r.policies[u.GroupID]
		if !ok {
			continue // unreadable group: previous rows kept
		}
		// The known countries are established on the group's own
		// min_days, the same bar sub_spread holds a province to.
		var known []string
		if pw := pl.users[u.ID]; pw != nil {
			known = domain.EstablishedCountries(pw.sightings, policy.risk.MinDays)
		}
		// The warm-up and the hold are the group's (already bounded by the
		// configured lookback in loadPolicies).
		v, ev := domain.EvaluateLoginCountry(
			domain.LoginCountryPolicy{
				Off:          policy.risk.LoginCountryOff,
				Geo:          policy.geo,
				WarmupLogins: policy.risk.LoginWarmupLogins,
				HoldDays:     policy.risk.LoginHoldDays,
			},
			domain.LoginCountryInput{
				NowMS:        nowMS,
				LookbackDays: lookback,
				GeoAvailable: pl.geoAvailable,
				Logins:       sightings[u.ID],
				Known:        known,
			})
		addVerdict(r, u.ID, domain.RiskKindLoginCountry, v, ev)
	}
	return nil
}

// readLogins pages through the successful logins since `since`, lowest id
// first, and keeps those of listed accounts.
//
// The pages are a keyset walk: each read asks for the first page after the
// highest id the read before it served (AfterID), never for page n by
// offset, because both ends of the log move while a run reads it. New
// logins land after the highest id; newest-first paging would push every
// row one place down, read one twice and never see the new one. The hourly
// retention prune deletes from the low end, and inside the read: with a
// retention no longer than the configured lookback the lookback IS the
// retention (loginLookbackDays), so the store's bound below is a day older
// than the prune's cutoff, and the band it cuts holds the read's lowest ids.
// Under offset paging every row after the deleted ones would move up past a
// page boundary and never be read, and a missed earlier login from a
// country makes a later one from there read as new — a flag raised on
// missing data. A cursor on the primary key is moved by neither end.
//
// The store's time bound is only a pre-filter — SQLite compares times as
// zone-bearing strings — so it is taken a day early, in UTC, as the
// fetch-log scan's is, and the evaluator cuts the lookback exactly. Failed
// attempts are not logins, and an attempt no account was resolved for
// (UserID 0) is nobody's.
func (s *Service) readLogins(ctx context.Context, r *refresh, since time.Time) (map[int64][]accountLogin, error) {
	bound := since.UTC().Add(-24 * time.Hour)
	listed := make(map[int64]bool, len(r.users))
	for _, u := range r.users {
		listed[u.ID] = true
	}
	out := map[int64][]accountLogin{}
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		events, _, err := s.d.AuthEvents.List(ctx, ports.AuthEventFilter{
			Pagination: ports.Pagination{Page: 1, PageSize: authPageSize, SortBy: "id", SortDir: "asc"},
			Outcome:    string(domain.AuthOutcomeSuccess),
			Since:      &bound,
			AfterID:    after,
		})
		if err != nil {
			return nil, err
		}
		// next is the highest id served: in the id order asked for, the
		// last row's.
		next := after
		for _, e := range events {
			if e == nil {
				continue
			}
			next = max(next, e.ID)
			if e.UserID > 0 && listed[e.UserID] {
				out[e.UserID] = append(out[e.UserID], accountLogin{atMS: e.At.UnixMilli(), method: string(e.Method), ip: e.IP})
			}
		}
		if len(events) < authPageSize {
			return out, nil
		}
		// The walk ends only on a short page. A full page that did not
		// pass the cursor means a store that ignored it, and would serve
		// the same page until shutdown while the run holds the read side
		// of the operation gate; a log read that way is unreadable.
		if next <= after {
			return nil, fmt.Errorf("login log: a full page after id %d served no later id: %w", after, domain.ErrUnavailable)
		}
		after = next
	}
}

// placeLogins turns each login into what the evaluator reads: a skip reason,
// or the country its address is in.
//
// An address is set aside by the fetch window's own rules, one address at a
// time (domain.AddressExclusion): internal ranges, the admin ignore list,
// PSP's own nodes and relays — the last because a login through a relay
// carries the relay's address. There is no shared-exit rule: a login is one
// account's act. What is left is placed with ONE geo lookup, which also
// places the landing nodes, and a login from a country a landing is in is
// skipped as a node country. Relays' countries are not node countries (see
// Deps.LandingAddrs). Without a geo database nothing is looked up and every
// login left reads unplaced; the evaluator answers geo_unavailable.
func (s *Service) placeLogins(ctx context.Context, pl placement, logins map[int64][]accountLogin) map[int64][]domain.LoginSighting {
	ex := domain.AddressExclusions{Internal: true, Ignore: pl.ignore, Infra: s.d.IsInfra}
	type pending struct {
		uid    int64
		i      int
		lookup string
	}
	out := make(map[int64][]domain.LoginSighting, len(logins))
	var todo []pending
	seen := map[string]bool{}
	var ips []string
	ask := func(ip string) {
		if !seen[ip] {
			seen[ip] = true
			ips = append(ips, ip)
		}
	}
	for uid, list := range logins {
		rows := make([]domain.LoginSighting, len(list))
		for i, l := range list {
			rows[i] = domain.LoginSighting{AtMS: l.atMS, Method: l.method}
			switch skip := domain.AddressExclusion(l.ip, ex); skip {
			case "":
				// The member itself (unmapped, zone-free), not its /64:
				// one login is one address.
				_, a, _ := domain.SourceKey(l.ip)
				todo = append(todo, pending{uid: uid, i: i, lookup: a.String()})
				ask(a.String())
			case domain.AddressUnparseable:
				rows[i].Skip = domain.LoginSkipUnplaced
			default:
				rows[i].Skip = skip
			}
		}
		out[uid] = rows
	}
	if !pl.geoAvailable || len(todo) == 0 {
		for _, p := range todo {
			out[p.uid][p.i].Skip = domain.LoginSkipUnplaced
		}
		return out
	}

	var landing []string
	if s.d.LandingAddrs != nil {
		for _, a := range s.d.LandingAddrs() {
			key := a.Unmap().WithZone("").String()
			landing = append(landing, key)
			ask(key)
		}
	}
	sort.Strings(ips)
	located := s.d.Geo.Lookup(ctx, ips)
	nodeCountries := map[string]bool{}
	for _, key := range landing {
		if cc, _ := placeOf(located[key]); cc != "" {
			nodeCountries[cc] = true
		}
	}
	for _, p := range todo {
		row := &out[p.uid][p.i]
		cc, _ := placeOf(located[p.lookup])
		switch {
		case cc == "":
			row.Skip = domain.LoginSkipUnplaced
		case nodeCountries[cc]:
			row.CC, row.Skip = cc, domain.LoginSkipNodeCountry
		default:
			row.CC = cc
		}
	}
	return out
}
