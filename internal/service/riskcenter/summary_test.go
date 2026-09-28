package riskcenter

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The drawer opens any account, including one that no longer exists: that
// is a not-found, never an empty summary of nobody.
func TestUserSummary_NotFound(t *testing.T) {
	h := newHarness()
	if _, err := h.svc.UserSummary(t.Context(), 404); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing account = %v, want ErrNotFound", err)
	}
}

// The review names the admins who acted by their CURRENT names, read in one
// lookup; an admin deleted since has no name ("" — the SPA shows the id).
// The account's group is named.
func TestUserSummary_ReviewNamesAndADeletedAdmin(t *testing.T) {
	h := newHarness()
	h.user(7, "alice")
	h.user(1, "root")
	h.users.byID[7].GroupID = 2
	h.groups.groups = []*domain.Group{{ID: 1, Name: "Default"}, {ID: 2, Name: "Team A"}}
	h.geo.rows = []domain.GeoRecord{geoAt(7, true, 0, domain.GeoStateFlagged, testNow)}
	h.dismiss(7, testNow.Add(-time.Minute), domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, testNow.Add(-2*time.Minute))})
	r := h.reviews.rows[7]
	r.DismissedBy, r.Trusted, r.TrustedBy, r.Note = 1, true, 2, "family trip"
	h.reviews.rows[7] = r

	s, err := h.svc.UserSummary(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if s.User == nil || s.User.UPN != "alice" || s.GroupName != "Team A" {
		t.Fatalf("summary names %+v in %q, want alice in Team A", s.User, s.GroupName)
	}
	if s.DismissedByUPN != "root" || s.TrustedByUPN != "" {
		t.Fatalf("dismissed by %q, trusted by %q; want root and \"\" (admin 2 is gone)", s.DismissedByUPN, s.TrustedByUPN)
	}
	if !s.Attention.HasReview || s.Attention.Review.Note != "family trip" || !s.Attention.Review.Trusted {
		t.Fatalf("attention review = %+v, want the stored row", s.Attention.Review)
	}
	var namedAdmins bool
	for _, ids := range h.users.listed {
		if slices.Equal(ids, []int64{1, 2}) {
			namedAdmins = true
		}
	}
	if !namedAdmins {
		t.Fatalf("names read for %v, want one lookup of the admins [1 2]", h.users.listed)
	}
}

// The drawer shows the account's verdicts in ANY state — a clean row is
// the answer to "is this account fine", not a gap — each marked stale when
// nobody re-judged it within the window the queue counts. A kind this
// build does not compute is left out; the rest read in RiskKinds order.
func TestUserSummary_StaleFlags(t *testing.T) {
	h := newHarness()
	h.settings.set = ports.UISettings{RiskAlertFreshnessHours: 2, CronTrafficPullMinutes: 120}
	h.user(7, "alice")
	geoSince, riskSince := testNow.Add(-4*time.Hour), testNow.Add(-2*time.Hour)
	h.geo.rows = []domain.GeoRecord{geoAt(7, false, 0, domain.GeoStateClean, geoSince.Add(-time.Millisecond))}
	h.signals.rows = []domain.RiskSignal{
		signalAt(7, domain.RiskKindUsageShift, domain.GeoStateClean, testNow),
		signalAt(7, domain.RiskKindSubSpread, domain.GeoStateFlagged, riskSince.Add(-time.Millisecond)),
		signalAt(7, "future_kind", domain.GeoStateFlagged, testNow),
		signalAt(7, domain.RiskKindDevices, domain.GeoStateSuspect, riskSince),
	}

	s, err := h.svc.UserSummary(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if s.Geo == nil || s.Geo.State != domain.GeoStateClean || !s.GeoStale {
		t.Fatalf("geo = %+v stale %v, want the clean row, stale", s.Geo, s.GeoStale)
	}
	var kinds []domain.RiskKind
	for _, sig := range s.Signals {
		kinds = append(kinds, sig.Kind)
	}
	if want := []domain.RiskKind{domain.RiskKindSubSpread, domain.RiskKindDevices, domain.RiskKindUsageShift}; !slices.Equal(kinds, want) {
		t.Fatalf("signals = %v, want %v (known kinds, RiskKinds order)", kinds, want)
	}
	if !s.StaleKinds[domain.RiskKindSubSpread] || s.StaleKinds[domain.RiskKindDevices] || s.StaleKinds[domain.RiskKindUsageShift] {
		t.Fatalf("stale kinds = %v, want sub_spread alone", s.StaleKinds)
	}
	want := domain.AttentionLevels{"devices": domain.FlagLevelSuspect}
	if len(s.Attention.Levels) != 1 || s.Attention.Levels["devices"] != want["devices"] {
		t.Fatalf("attention = %v, want devices suspect alone (sub_spread is stale)", s.Attention.Levels)
	}

	h.geo.rows = nil
	if s, err = h.svc.UserSummary(t.Context(), 7); err != nil || s.Geo != nil || s.GeoStale {
		t.Fatalf("no geo row: geo %+v stale %v (%v), want nil and not stale", s.Geo, s.GeoStale, err)
	}
}

// The devices are an inference on top of the rest; a fetch log that cannot
// be read costs the devices (DevicesUnavailable), never the summary.
func TestUserSummary_DevicesUnavailableIsNotAnError(t *testing.T) {
	h := newHarness()
	h.user(7, "alice")
	h.fetches.err = errors.New("sub log unreadable")

	s, err := h.svc.UserSummary(t.Context(), 7)
	if err != nil {
		t.Fatalf("a fetch-log error failed the summary: %v", err)
	}
	if !s.DevicesUnavailable || len(s.Devices) != 0 {
		t.Fatalf("devices %+v unavailable %v, want none and unavailable", s.Devices, s.DevicesUnavailable)
	}
}

// The live part is the live view limited to the ONE account (the same view
// GET /live?user_id= serves), and the devices are that account's fetches
// over the same device window, grouped per client.
func TestUserSummary_LiveLimitedToTheAccount(t *testing.T) {
	h := newHarness()
	h.user(7, "alice")
	h.user(8, "bob")
	h.live.snap = pollSnapshot(testNow.Add(-time.Minute),
		conn(7, 1, "198.51.100.1", ""), conn(7, 2, "198.51.100.2", ""), conn(8, 1, "198.51.100.3", ""))
	h.fetches.rows = []domain.SubLog{
		{ID: 1, UserID: 7, IP: "198.51.100.1", UA: "clash.meta/1.19", ClientType: "mihomo", AccessedAt: testNow.Add(-time.Hour)},
		{ID: 2, UserID: 7, IP: "203.0.113.9", UA: "v2rayN/7.0", ClientType: "uri-list", AccessedAt: testNow.Add(-2 * time.Hour)},
	}

	s, err := h.svc.UserSummary(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := userIDs(s.Live); !slices.Equal(got, []int64{7}) || s.Live.Total != 1 || len(s.Live.Users[0].Conns) != 2 {
		t.Fatalf("live = %v of %d, want account 7 alone with its 2 connections", got, s.Live.Total)
	}
	if !slices.Equal(h.fetches.gotIDs, []int64{7}) || !h.fetches.gotSince.Equal(testNow.Add(-24*time.Hour)) || h.fetches.gotLimit != deviceInferMaxRows {
		t.Fatalf("fetches read for %v since %v (limit %d), want [7] over the 24h window", h.fetches.gotIDs, h.fetches.gotSince, h.fetches.gotLimit)
	}
	if s.DeviceWindow != 24*time.Hour || s.DevicesUnavailable || len(s.Devices) != 2 || s.Devices[0].UA != "clash.meta/1.19" {
		t.Fatalf("devices %+v (window %v), want both clients, the newest first", s.Devices, s.DeviceWindow)
	}
}
