package destlist

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func (s *Service) effective(ctx context.Context) ports.AccessControlEffective {
	if s.settings == nil {
		return ports.AccessControlEffectiveOf(ports.UISettings{})
	}
	set, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return ports.AccessControlEffectiveOf(ports.UISettings{})
	}
	return ports.AccessControlEffectiveOf(set)
}

// RefreshInterval is dest_list_refresh_hours as it stands NOW. It is read at
// every call, which is what lets an admin shorten it without a restart: the
// loop asks before each sleep.
func (s *Service) RefreshInterval(ctx context.Context) time.Duration {
	return s.effective(ctx).ListRefresh()
}

// RefreshDue is one pass of the dest-list-refresh loop, and the only thing the
// loop does:
//
//   - every remote list that has not been fetched for the refresh interval is
//     fetched again (a failure keeps its entries and records why);
//   - the upstream geosite release is probed (at most every six hours) and, in
//     auto mode, a newer one is applied;
//   - the GeoIP database is compared with the one the last pass saw, and a
//     change advances the policy generation when a rule uses a country.
//
// Single-flight: a pass that starts while another runs returns at once.
func (s *Service) RefreshDue(ctx context.Context) {
	if !s.refreshMu.TryLock() {
		return
	}
	defer s.refreshMu.Unlock()

	eff := s.effective(ctx)
	lists, err := s.lists.List(ctx, false)
	if err != nil {
		log.Warn("destlist: refresh could not list the lists", "err", err)
		return
	}
	now := s.now()
	for _, l := range lists {
		if ctx.Err() != nil {
			return
		}
		if l.Kind != domain.DestListRemote {
			continue
		}
		if l.LastFetchedAt != nil && now.Sub(*l.LastFetchedAt) < eff.ListRefresh() {
			continue
		}
		out := s.refreshRemote(ctx, l)
		if out.Result == RefreshFailed || out.Result == RefreshBroad {
			// The id and the class, never the URL's query or the reason's
			// detail beyond its code: a list URL may carry a token.
			log.Warn("destlist: remote list refresh failed", "list_id", l.ID, "result", out.Result)
		}
	}
	// The local check first: the geosite probe below is a network call.
	s.geoDatabaseTick(ctx)
	s.geositeTick(ctx, eff, lists)
}

// geositeTick probes the upstream and, in auto mode, applies a newer release.
// A release that was tried and refused is not tried again for the probe window:
// the refusal is about THIS release's content, and re-downloading it every
// round would change nothing.
//
// A panel that does not use geosite lists never asks GitHub anything: the
// probe happens only once geosite data has been downloaded or a geosite list
// exists. A feature nobody turned on must not phone home on its own, and the
// admin's own actions (adding a template, "update") probe for themselves.
func (s *Service) geositeTick(ctx context.Context, eff ports.AccessControlEffective, lists []*domain.DestList) {
	if cur, _ := s.currentGeo(ctx); cur == nil && !slices.ContainsFunc(lists, func(l *domain.DestList) bool { return l.Kind == domain.DestListGeosite }) {
		return
	}
	latest, err := s.CheckLatest(ctx, false)
	if err != nil {
		log.Warn("destlist: geosite upstream check failed", "err", err)
		return
	}
	if eff.RulesUpdateMode != ports.DestRulesUpdateAuto || latest.Tag == "" {
		return
	}
	_, cur := s.currentGeo(ctx)
	if cur == nil || cur.Tag == latest.Tag {
		// With no data there is nothing to update: the first download is an
		// explicit act (a template, or the admin's own).
		return
	}
	s.geo.mu.Lock()
	tried := s.autoTag == latest.Tag && s.geo.now().Sub(s.autoAt) < geositeThrottle
	if !tried {
		s.autoTag, s.autoAt = latest.Tag, s.geo.now()
	}
	s.geo.mu.Unlock()
	if tried {
		return
	}
	res, err := s.GeositeUpdate(ctx, UpdateOptions{Tag: latest.Tag})
	switch {
	case err != nil:
		log.Warn("destlist: automatic geosite update refused", "tag", latest.Tag, "err", err)
	case res.Applied:
		log.Info("destlist: geosite data updated", "tag", latest.Tag, "lists_changed", res.ListsChanged, "published", res.Bumped)
	}
}

// geoDatabaseTick notices a GeoIP database that is not the one the policy was
// last reconciled with. What a country expands to is a property of that file,
// so when it changes — appears, vanishes or is replaced — a rule that denies a
// country now means different addresses (or none, and is skipped): the
// generation advances, and only when something in force uses a country, so a
// database that refreshes monthly does not restart every core for nothing.
//
// The digest compared against is the PERSISTED one (dest_policy_state), never
// a first sighting held in memory: a database replaced while the panel was
// down, or the first one after a stretch with none, differs from it just the
// same. When nothing uses a country the digest is still recorded, so that a
// rule added later is judged against the database it was added beside.
func (s *Service) geoDatabaseTick(ctx context.Context) {
	if s.sourceRules == nil || s.state == nil {
		return
	}
	// "No database" (no active path) is the empty digest — a real state a rule
	// changes meaning across. A database that is there but cannot be read right
	// now is NOT: guessing "" for it would republish a policy with every country
	// rule skipped over a transient read error, so that pass is skipped.
	sum := ""
	if s.Countries.ActivePath(ctx) != "" {
		var err error
		if sum, err = s.Countries.DatabaseSHA256(ctx); err != nil {
			log.Warn("destlist: the GeoIP database could not be read; skipping the change check", "err", err)
			return
		}
	}
	st, err := s.state.Get(ctx)
	if err != nil {
		log.Warn("destlist: could not read the GeoIP digest", "err", err)
		return
	}
	if st.GeoIPSHA256 == sum {
		return
	}
	inUse, err := s.countriesInUse(ctx)
	if err != nil {
		log.Warn("destlist: could not tell whether a GeoIP change matters", "err", err)
		return
	}
	if err := s.state.ReconcileGeoIP(ctx, sum, inUse); err != nil {
		log.Warn("destlist: could not record a GeoIP database change", "err", err)
		return
	}
	if inUse {
		log.Info("destlist: the GeoIP database changed under a rule that uses a country; policy republished")
	}
}

func (s *Service) countriesInUse(ctx context.Context) (bool, error) {
	rules, err := s.sourceRules.List(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		if r.State != domain.DestStateOff && len(r.Countries) > 0 {
			return true, nil
		}
	}
	policies, err := s.policies.List(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range policies {
		if p.State == domain.DestStateOff {
			continue
		}
		for _, id := range p.ListIDs {
			entries, err := s.lists.Entries(ctx, id)
			if err != nil {
				continue
			}
			if strings.HasPrefix(string(entries), "geoip:") || strings.Contains(string(entries), "\ngeoip:") {
				return true, nil
			}
		}
	}
	return false, nil
}
