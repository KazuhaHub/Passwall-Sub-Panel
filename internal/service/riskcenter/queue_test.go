package riskcenter

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- fixtures ----

// geoAt is a geo_streaks row judged at, latched and/or over tolerance. The
// state is what an account in that streak would read; the queue reads the
// streak, never the state.
func geoAt(uid int64, flagged bool, over int, state domain.GeoState, at time.Time) domain.GeoRecord {
	return domain.GeoRecord{
		UserID: uid, State: state, Places: []string{"DE", "JP"},
		Streak:      domain.GeoStreak{Flagged: flagged, Over: over},
		UpdatedAtMS: at.UnixMilli(),
	}
}

// signalAt is a risk_signals row written at.
func signalAt(uid int64, kind domain.RiskKind, state domain.GeoState, at time.Time) domain.RiskSignal {
	return domain.RiskSignal{UserID: uid, Kind: kind, State: state, Code: "c", UpdatedAtMS: at.UnixMilli()}
}

// hold puts a service hold with reason on the account, written at.
func (h *harness) hold(uid int64, reason domain.AutoDisabledReason, at time.Time) {
	h.users.byID[uid].ServiceDisabledReason = reason
	h.users.byID[uid].ServiceDisabledAt = &at
}

// dismiss stores a dismissal made at, accepting snap.
func (h *harness) dismiss(uid int64, at time.Time, snap domain.DismissSnapshot) {
	r := h.reviews.rows[uid]
	r.UserID, r.DismissedAtMS, r.DismissedBy, r.Accepted = uid, at.UnixMilli(), 1, snap
	h.reviews.rows[uid] = r
}

// trust stores a trust.
func (h *harness) trust(uid int64) {
	r := h.reviews.rows[uid]
	r.UserID, r.Trusted, r.TrustedAtMS, r.TrustedBy = uid, true, testNow.Add(-time.Hour).UnixMilli(), 1
	h.reviews.rows[uid] = r
}

// accepted is one snapshot entry.
func accepted(l domain.FlagLevel, at time.Time) domain.AcceptedLevel {
	return domain.AcceptedLevel{Level: l, AtMS: at.UnixMilli()}
}

func rowIDs(v QueueView) []int64 {
	out := make([]int64, 0, len(v.Rows))
	for _, r := range v.Rows {
		out = append(out, r.User.ID)
	}
	return out
}

func (h *harness) queue(t *testing.T, q QueueQuery) QueueView {
	t.Helper()
	v, err := h.svc.Queue(t.Context(), q)
	if err != nil {
		t.Fatalf("queue %+v: %v", q, err)
	}
	return v
}

func rowOf(t *testing.T, v QueueView, uid int64) QueueRow {
	t.Helper()
	for _, r := range v.Rows {
		if r.User.ID == uid {
			return r
		}
	}
	t.Fatalf("account %d is not listed (rows %v)", uid, rowIDs(v))
	return QueueRow{}
}

// ---- the attention computation ----

// One row per ACCOUNT, carrying every source it is at attention on: the geo
// verdict, each risk kind at suspect or flagged, and the detector's own
// suspension — each with the time of the verdict it was read from (what a
// dismissal snapshots). A kind that is clean is not a source, and its
// evidence is not on the row.
func TestQueue_OneRowPerAccountWithEverySource(t *testing.T) {
	h := newHarness()
	h.user(7, "alice")
	h.user(8, "bob")
	h.users.byID[7].GroupID = 2
	h.groups.groups = []*domain.Group{{ID: 2, Name: "Team A"}}
	geoT, subT, devT, holdT := testNow.Add(-time.Minute), testNow.Add(-2*time.Minute), testNow.Add(-3*time.Minute), testNow.Add(-time.Hour)
	h.geo.rows = []domain.GeoRecord{geoAt(7, true, 3, domain.GeoStateFlagged, geoT)}
	h.signals.rows = []domain.RiskSignal{
		signalAt(7, domain.RiskKindSubSpread, domain.GeoStateSuspect, subT),
		signalAt(7, domain.RiskKindDevices, domain.GeoStateFlagged, devT),
		signalAt(7, domain.RiskKindUsageShift, domain.GeoStateClean, devT),
		signalAt(8, domain.RiskKindUsageShift, domain.GeoStateSuspect, devT),
	}
	h.hold(7, domain.DisabledGeoAutoSuspend, holdT)

	v := h.queue(t, QueueQuery{})

	if v.Total != 2 || len(v.Rows) != 2 {
		t.Fatalf("rows %v of %d, want one per account: 7 and 8", rowIDs(v), v.Total)
	}
	a := rowOf(t, v, 7)
	want := domain.AttentionLevels{
		"geo": domain.FlagLevelFlagged, "sub_spread": domain.FlagLevelSuspect,
		"devices": domain.FlagLevelFlagged, "geo_auto": domain.FlagLevelSuspended,
	}
	if !reflect.DeepEqual(a.Attention.Levels, want) {
		t.Fatalf("levels = %v, want %v", a.Attention.Levels, want)
	}
	wantAt := map[string]int64{"geo": geoT.UnixMilli(), "sub_spread": subT.UnixMilli(), "devices": devT.UnixMilli(), "geo_auto": holdT.UnixMilli()}
	if !reflect.DeepEqual(a.Attention.AtMS, wantAt) || a.Attention.HeldSinceMS != holdT.UnixMilli() {
		t.Fatalf("verdict times %v, held since %d; want %v and the hold's time", a.Attention.AtMS, a.Attention.HeldSinceMS, wantAt)
	}
	if a.Geo == nil || a.Geo.UserID != 7 {
		t.Fatalf("geo evidence = %+v, want account 7's row", a.Geo)
	}
	var kinds []domain.RiskKind
	for _, s := range a.Signals {
		kinds = append(kinds, s.Kind)
	}
	if !slices.Equal(kinds, []domain.RiskKind{domain.RiskKindSubSpread, domain.RiskKindDevices}) {
		t.Fatalf("signals = %v, want the attention kinds only, in RiskKinds order", kinds)
	}
	if a.GroupName != "Team A" || a.User.UPN != "alice" {
		t.Fatalf("row names %q in %q, want alice in Team A", a.User.UPN, a.GroupName)
	}
	if b := rowOf(t, v, 8); b.Geo != nil || len(b.Signals) != 1 || b.Attention.Levels["usage_shift"] != domain.FlagLevelSuspect {
		t.Fatalf("account 8 = %+v, want usage_shift suspect and no geo", b)
	}
}

// Attention is the level of a FRESH verdict, exactly as the bell counts it:
// a geo row the poll re-judged within GeoBellFreshness (the alert freshness,
// floored at two polls), a risk row the worker rewrote within AlertFreshness.
// The bound is inclusive. A latched flag nobody re-judged is history.
func TestQueue_FreshnessDropsStaleVerdicts(t *testing.T) {
	h := newHarness()
	// Risk freshness 2h; geo freshness max(2h, two 2-hour polls) = 4h.
	h.settings.set = ports.UISettings{RiskAlertFreshnessHours: 2, CronTrafficPullMinutes: 120}
	for id := int64(1); id <= 5; id++ {
		h.user(id, "u")
	}
	geoSince, riskSince := testNow.Add(-4*time.Hour), testNow.Add(-2*time.Hour)
	h.geo.rows = []domain.GeoRecord{
		geoAt(1, true, 0, domain.GeoStateIdle, testNow.Add(-3*time.Hour)),       // in: inside the geo window
		geoAt(2, true, 0, domain.GeoStateIdle, geoSince.Add(-time.Millisecond)), // out
		geoAt(5, true, 0, domain.GeoStateIdle, geoSince),                        // in: at the bound
	}
	h.signals.rows = []domain.RiskSignal{
		signalAt(3, domain.RiskKindDevices, domain.GeoStateFlagged, testNow.Add(-3*time.Hour)), // out: outside the risk window
		signalAt(4, domain.RiskKindDevices, domain.GeoStateFlagged, riskSince),                 // in: at the bound
	}

	v := h.queue(t, QueueQuery{})

	if got := rowIDs(v); !slices.Equal(got, []int64{1, 4, 5}) {
		t.Fatalf("rows = %v, want [1 4 5]", got)
	}
	if !h.geo.gotSince.Equal(geoSince) || !h.signals.gotSince.Equal(riskSince) {
		t.Fatalf("read geo since %v and risk since %v, want %v and %v", h.geo.gotSince, h.signals.gotSince, geoSince, riskSince)
	}
}

// Geo attention is the STREAK: latched is flagged even while the account is
// idle (it disconnected, and disconnecting must not clear it), mid-ramp is
// suspect, and a row at neither is not listed whatever its state says.
func TestQueue_LatchedIdleGeoIsFlagged(t *testing.T) {
	h := newHarness()
	for id := int64(1); id <= 3; id++ {
		h.user(id, "u")
	}
	h.geo.rows = []domain.GeoRecord{
		geoAt(1, true, 0, domain.GeoStateIdle, testNow),
		geoAt(2, false, 1, domain.GeoStateIdle, testNow),
		geoAt(3, false, 0, domain.GeoStateClean, testNow),
	}

	v := h.queue(t, QueueQuery{})

	if got := rowIDs(v); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("rows = %v, want [1 2]", got)
	}
	if l := rowOf(t, v, 1).Attention.Levels["geo"]; l != domain.FlagLevelFlagged {
		t.Fatalf("latched idle = %q, want flagged", l)
	}
	if l := rowOf(t, v, 2).Attention.Levels["geo"]; l != domain.FlagLevelSuspect {
		t.Fatalf("ramping idle = %q, want suspect", l)
	}
}

// The detector's own suspension is a source of its own (geo_auto,
// suspended), read from the users row, and makes the account urgent on its
// own. Somebody else's hold is not the detector's and is not a source.
func TestQueue_GeoAutoHoldIsASource(t *testing.T) {
	h := newHarness()
	h.user(1, "held")
	h.user(2, "manual")
	at := testNow.Add(-30 * time.Minute)
	h.hold(1, domain.DisabledGeoAutoSuspend, at)
	h.hold(2, domain.DisabledServiceManual, at)

	v := h.queue(t, QueueQuery{})

	if got := rowIDs(v); !slices.Equal(got, []int64{1}) {
		t.Fatalf("rows = %v, want [1]", got)
	}
	a := rowOf(t, v, 1).Attention
	if !a.Levels.Held() || a.Levels.Max() != domain.FlagLevelNone || !a.Urgent() || a.HeldSinceMS != at.UnixMilli() {
		t.Fatalf("held account = %+v, want held, no verdict level, urgent, held since the hold", a)
	}
	if h.holds.gotReason != domain.DisabledGeoAutoSuspend {
		t.Fatalf("listed holds of %q, want geo_auto", h.holds.gotReason)
	}
}

// A trusted account's location sources are dropped at read time — the
// detectors re-judge it exempt only on their next run, and a trust that
// left the account in the queue until then would read as "trust did
// nothing". Devices and usage are not about location and stay.
func TestQueue_TrustMasksLocationSources(t *testing.T) {
	h := newHarness()
	h.user(7, "traveller")
	h.user(8, "geo-only")
	h.geo.rows = []domain.GeoRecord{geoAt(7, true, 0, domain.GeoStateFlagged, testNow), geoAt(8, true, 0, domain.GeoStateFlagged, testNow)}
	h.signals.rows = []domain.RiskSignal{
		signalAt(7, domain.RiskKindSubSpread, domain.GeoStateFlagged, testNow),
		signalAt(7, domain.RiskKindLoginCountry, domain.GeoStateSuspect, testNow),
		signalAt(7, domain.RiskKindDevices, domain.GeoStateSuspect, testNow),
	}
	h.trust(7)
	h.trust(8)

	v := h.queue(t, QueueQuery{})

	if got := rowIDs(v); !slices.Equal(got, []int64{7}) {
		t.Fatalf("open rows = %v, want [7] (8 has nothing left once masked)", got)
	}
	a := rowOf(t, v, 7)
	if !reflect.DeepEqual(a.Attention.Levels, domain.AttentionLevels{"devices": domain.FlagLevelSuspect}) {
		t.Fatalf("levels = %v, want devices alone", a.Attention.Levels)
	}
	if _, ok := a.Attention.AtMS["geo"]; ok || len(a.Attention.AtMS) != 1 {
		t.Fatalf("verdict times = %v, want devices' alone", a.Attention.AtMS)
	}
	if a.Geo != nil || len(a.Signals) != 1 {
		t.Fatalf("evidence geo %+v, signals %+v; want no geo and the devices signal only", a.Geo, a.Signals)
	}
}

// A dismissal hides the account from 待处理 and lists it under 已忽略 —
// until something new or worse happens, read from the flag records since
// each source's verdict time: then it is open again, reopened, naming the
// source that escalated.
func TestQueue_DismissedLeavesOpenUntilReopened(t *testing.T) {
	h := newHarness()
	h.user(7, "alice")
	geoT, d := testNow.Add(-2*time.Hour), testNow.Add(-time.Hour)
	h.geo.rows = []domain.GeoRecord{geoAt(7, true, 0, domain.GeoStateFlagged, testNow)}
	h.dismiss(7, d, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, geoT)})

	if v := h.queue(t, QueueQuery{}); len(v.Rows) != 0 {
		t.Fatalf("open = %v, want none: the dismissal covers geo flagged", rowIDs(v))
	}
	v := h.queue(t, QueueQuery{Status: "dismissed"})
	if got := rowIDs(v); !slices.Equal(got, []int64{7}) || !rowOf(t, v, 7).Attention.DismissedInForce() {
		t.Fatalf("dismissed = %v, want [7] in force", got)
	}

	// A new source after the dismissal reopens it.
	h.signals.rows = []domain.RiskSignal{signalAt(7, domain.RiskKindDevices, domain.GeoStateSuspect, testNow)}
	h.flags.steps = map[int64][]domain.FlagStep{7: {{Source: "devices", Level: domain.FlagLevelSuspect, State: domain.GeoStateSuspect, AtMS: d.Add(time.Minute).UnixMilli()}}}

	v = h.queue(t, QueueQuery{})
	if got := rowIDs(v); !slices.Equal(got, []int64{7}) {
		t.Fatalf("open = %v, want [7] reopened", got)
	}
	st := rowOf(t, v, 7).Attention.State
	if !st.Dismissed || !st.Reopened || !slices.Equal(st.Escalated, []string{"devices"}) {
		t.Fatalf("review state = %+v, want dismissed, reopened by devices", st)
	}
	if v := h.queue(t, QueueQuery{Status: "dismissed"}); len(v.Rows) != 0 {
		t.Fatalf("dismissed = %v, want none once reopened", rowIDs(v))
	}
}

// A dismissal older than the flag-record retention may have lost its
// history to the prune, so it can no longer prove nothing happened: it
// lapses and the account is open again. Its records are not even read.
func TestQueue_LapsedDismissalIsOpen(t *testing.T) {
	h := newHarness()
	h.settings.set = ports.UISettings{RiskFlagRecordRetentionDays: 1}
	h.user(7, "alice")
	old := testNow.Add(-48 * time.Hour)
	h.geo.rows = []domain.GeoRecord{geoAt(7, true, 0, domain.GeoStateFlagged, testNow)}
	h.dismiss(7, old, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, old.Add(-time.Minute))})

	v := h.queue(t, QueueQuery{})

	if got := rowIDs(v); !slices.Equal(got, []int64{7}) {
		t.Fatalf("open = %v, want the lapsed account [7]", got)
	}
	if st := rowOf(t, v, 7).Attention.State; !st.Lapsed || !st.Reopened {
		t.Fatalf("review state = %+v, want lapsed and reopened", st)
	}
	if len(h.flags.stepsAsked) != 0 {
		t.Fatalf("read the records of %v, want none: a lapsed dismissal's history is not consulted", h.flags.stepsAsked)
	}
}

// The retention decides how far back the prune deletes from now on; raising
// it cannot bring back what a shorter one already deleted. So a dismissal
// whose own record the prune took lapses whatever the setting says now:
// retention 30 days, a dismissal 45 days old with its history gone, the
// admin raises the retention to 90 — the dismissal must not start covering
// the account again. The queue and the drawer read it alike; with its record
// still kept it covers, and with nothing kept at all it lapses. Nothing is
// asked of the records' age while no dismissal is stored.
func TestQueue_DismissalLapsesWhenItsRecordsWerePrunedBeforeARaise(t *testing.T) {
	h := newHarness()
	h.settings.set = ports.UISettings{RiskFlagRecordRetentionDays: 90}
	h.user(7, "alice")
	d := testNow.Add(-45 * 24 * time.Hour)
	h.geo.rows = []domain.GeoRecord{geoAt(7, false, 1, domain.GeoStateSuspect, testNow)}
	h.queue(t, QueueQuery{})
	if h.flags.oldestAsked != 0 {
		t.Fatalf("read the oldest record %d times with no dismissal stored, want none", h.flags.oldestAsked)
	}
	h.dismiss(7, d, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelSuspect, d.Add(-time.Minute))})
	h.flags.oldest = testNow.Add(-30 * 24 * time.Hour).UnixMilli()

	v := h.queue(t, QueueQuery{})
	if got := rowIDs(v); !slices.Equal(got, []int64{7}) {
		t.Fatalf("open = %v, want [7]: its history was pruned before the raise", got)
	}
	if st := rowOf(t, v, 7).Attention.State; !st.Lapsed || !st.Reopened {
		t.Fatalf("review state = %+v, want lapsed and reopened", st)
	}
	if len(h.flags.stepsAsked) != 0 {
		t.Fatalf("read the records of %v, want none: a lapsed dismissal's history is not consulted", h.flags.stepsAsked)
	}
	a, err := h.svc.UserAttention(t.Context(), 7)
	if err != nil || !a.State.Lapsed || !a.Open() {
		t.Fatalf("drawer: %+v, %v; want lapsed and open, as the queue", a.State, err)
	}

	h.flags.oldest = d.UnixMilli()
	if v := h.queue(t, QueueQuery{Status: "dismissed"}); !slices.Equal(rowIDs(v), []int64{7}) ||
		!rowOf(t, v, 7).Attention.DismissedInForce() {
		t.Fatalf("dismissed = %v, want [7] in force: its own record is still kept", rowIDs(v))
	}

	h.flags.oldestNone = true
	if st := rowOf(t, h.queue(t, QueueQuery{}), 7).Attention.State; !st.Lapsed {
		t.Fatalf("review state = %+v with no record stored at all, want lapsed", st)
	}
}

// The reopen rule's records are read ONLY for accounts it can change: a
// dismissal in force (not lapsed) over current attention. Not for an
// account never dismissed, nor one dismissed with nothing to cover, nor one
// only trusted — and in one call, from each account's own oldest cutoff.
func TestQueue_StepsReadOnlyForDismissedAccountsWithAttention(t *testing.T) {
	h := newHarness()
	for id := int64(1); id <= 5; id++ {
		h.user(id, "u")
	}
	h.settings.set = ports.UISettings{RiskFlagRecordRetentionDays: 1}
	d, verdict := testNow.Add(-time.Hour), testNow.Add(-2*time.Hour)
	h.geo.rows = []domain.GeoRecord{
		geoAt(1, true, 0, domain.GeoStateFlagged, testNow), // dismissed with attention: asked
		geoAt(3, true, 0, domain.GeoStateFlagged, testNow), // never dismissed
		geoAt(5, true, 0, domain.GeoStateFlagged, testNow), // dismissed long ago: lapsed
	}
	h.signals.rows = []domain.RiskSignal{signalAt(4, domain.RiskKindDevices, domain.GeoStateSuspect, testNow)}
	h.dismiss(1, d, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, verdict)})
	h.dismiss(2, d, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, verdict)}) // nothing now
	h.trust(4)
	h.dismiss(5, testNow.Add(-72*time.Hour), domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, testNow.Add(-73*time.Hour))})

	h.queue(t, QueueQuery{Status: "all"})

	want := []map[int64]int64{{1: verdict.UnixMilli()}}
	if !reflect.DeepEqual(h.flags.stepsAsked, want) {
		t.Fatalf("records asked %v, want one call for account 1 from its verdict time %v", h.flags.stepsAsked, want)
	}

	h2 := newHarness()
	h2.user(3, "u")
	h2.geo.rows = []domain.GeoRecord{geoAt(3, true, 0, domain.GeoStateFlagged, testNow)}
	h2.queue(t, QueueQuery{})
	if len(h2.flags.stepsAsked) != 0 {
		t.Fatalf("records asked %v with no dismissal anywhere, want no read", h2.flags.stepsAsked)
	}
}

// filterFleet is the fleet the filter tests share:
//
//	1 geo flagged           — open, urgent
//	2 devices suspect       — open
//	3 geo_auto hold only    — open, urgent, held
//	4 sub_spread flagged    — dismissed in force
//	5 devices flagged       — open, urgent; trusted
//	6 geo_auto hold         — dismissed in force (the hold accepted)
func filterFleet(t *testing.T) *harness {
	t.Helper()
	h := newHarness()
	for id := int64(1); id <= 6; id++ {
		h.user(id, "u")
	}
	holdT := testNow.Add(-time.Hour)
	h.geo.rows = []domain.GeoRecord{geoAt(1, true, 0, domain.GeoStateFlagged, testNow)}
	h.signals.rows = []domain.RiskSignal{
		signalAt(2, domain.RiskKindDevices, domain.GeoStateSuspect, testNow),
		signalAt(4, domain.RiskKindSubSpread, domain.GeoStateFlagged, testNow),
		signalAt(5, domain.RiskKindDevices, domain.GeoStateFlagged, testNow),
	}
	h.hold(3, domain.DisabledGeoAutoSuspend, holdT)
	h.hold(6, domain.DisabledGeoAutoSuspend, holdT)
	h.dismiss(4, testNow.Add(-time.Minute), domain.DismissSnapshot{"sub_spread": accepted(domain.FlagLevelFlagged, testNow.Add(-2*time.Minute))})
	h.dismiss(6, testNow.Add(-time.Minute), domain.DismissSnapshot{"geo_auto": accepted(domain.FlagLevelSuspended, holdT)})
	h.trust(5)
	return h
}

// Status picks the review state (待处理 by default), and the source, level,
// hold and urgent filters narrow it, as the metric cards and the bell ask:
// every filter composes with every other.
func TestQueue_StatusSourceLevelAutoUrgentFilters(t *testing.T) {
	h := filterFleet(t)
	for _, c := range []struct {
		name string
		q    QueueQuery
		want []int64
	}{
		{"open by default", QueueQuery{}, []int64{1, 5, 3, 2}},
		{"open", QueueQuery{Status: "open"}, []int64{1, 5, 3, 2}},
		{"dismissed", QueueQuery{Status: "dismissed"}, []int64{4, 6}},
		{"trusted", QueueQuery{Status: "trusted"}, []int64{5}},
		{"all", QueueQuery{Status: "all"}, []int64{1, 4, 5, 3, 6, 2}},
		{"source geo", QueueQuery{Sources: []string{"geo"}}, []int64{1}},
		{"sources over all", QueueQuery{Status: "all", Sources: []string{"devices", "sub_spread"}}, []int64{4, 5, 2}},
		{"source geo_auto", QueueQuery{Sources: []string{"geo_auto"}}, []int64{3}},
		{"level suspect", QueueQuery{Level: domain.FlagLevelSuspect}, []int64{2}},
		{"level flagged", QueueQuery{Level: domain.FlagLevelFlagged}, []int64{1, 5}},
		{"auto-suspended over all", QueueQuery{Status: "all", AutoSuspended: true}, []int64{3, 6}},
		{"urgent", QueueQuery{Urgent: true}, []int64{1, 5, 3}},
		{"urgent and flagged", QueueQuery{Urgent: true, Level: domain.FlagLevelFlagged}, []int64{1, 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := h.queue(t, c.q)
			if got := rowIDs(v); !slices.Equal(got, c.want) || v.Total != len(c.want) {
				t.Fatalf("rows %v of %d, want %v", got, v.Total, c.want)
			}
		})
	}
}

// 已信任 lists EVERY trusted account — trust is a standing exemption and
// must be findable to be withdrawn — including one with no attention at
// all, which is also under 全部.
func TestQueue_TrustedStatusListsEveryTrustedAccount(t *testing.T) {
	h := newHarness()
	h.user(5, "with-devices")
	h.user(6, "quiet")
	h.signals.rows = []domain.RiskSignal{signalAt(5, domain.RiskKindDevices, domain.GeoStateSuspect, testNow)}
	h.trust(5)
	h.trust(6)

	v := h.queue(t, QueueQuery{Status: "trusted"})

	if got := rowIDs(v); !slices.Equal(got, []int64{5, 6}) || v.Counts.Trusted != 2 {
		t.Fatalf("trusted = %v (count %d), want [5 6] and 2", got, v.Counts.Trusted)
	}
	quiet := rowOf(t, v, 6)
	if !quiet.Attention.Levels.Empty() || quiet.Attention.Levels.Max() != domain.FlagLevelNone ||
		len(quiet.Attention.Levels.Sources()) != 0 || quiet.Geo != nil || len(quiet.Signals) != 0 || quiet.Attention.Urgent() {
		t.Fatalf("a trusted account with no attention = %+v, want no level, no source, no evidence", quiet)
	}
	if got := rowIDs(h.queue(t, QueueQuery{Status: "all"})); !slices.Contains(got, 6) {
		t.Fatalf("all = %v, want the quiet trusted account too", got)
	}
}

// The search is a case-insensitive substring of the UPN or the display
// name, applied after the review filters; the counts do not move with it.
func TestQueue_SearchMatchesUPNAndDisplayName(t *testing.T) {
	h := newHarness()
	h.user(1, "alice")
	h.user(2, "bob")
	h.users.byID[1].DisplayName = "Alice Wong"
	h.users.byID[2].DisplayName = "Robert"
	h.geo.rows = []domain.GeoRecord{geoAt(1, true, 0, domain.GeoStateFlagged, testNow), geoAt(2, true, 0, domain.GeoStateFlagged, testNow)}

	for q, want := range map[string][]int64{"ALI": {1}, "robert": {2}, "  wong ": {1}, "zzz": {}, "": {1, 2}} {
		v := h.queue(t, QueueQuery{Q: q})
		if got := rowIDs(v); !slices.Equal(got, want) || v.Total != len(want) {
			t.Fatalf("q %q = %v of %d, want %v", q, got, v.Total, want)
		}
		if v.Counts.Flagged != 2 {
			t.Fatalf("q %q moved the counts: flagged %d, want 2", q, v.Counts.Flagged)
		}
	}
}

// The order an admin works in: flagged and held first, then flagged, then
// held alone (the bell rings for it), then suspect; within a class the
// latest change first; then the id, so a page boundary never reorders.
func TestQueue_SortOrder(t *testing.T) {
	h := newHarness()
	for id := int64(10); id <= 16; id++ {
		h.user(id, "u")
	}
	holdT := testNow.Add(-3 * time.Hour)
	h.geo.rows = []domain.GeoRecord{
		geoAt(10, true, 0, domain.GeoStateFlagged, testNow), geoAt(11, true, 0, domain.GeoStateFlagged, testNow),
		geoAt(12, true, 0, domain.GeoStateFlagged, testNow), geoAt(13, true, 0, domain.GeoStateFlagged, testNow),
		geoAt(14, true, 0, domain.GeoStateFlagged, testNow), geoAt(16, false, 1, domain.GeoStateSuspect, testNow),
	}
	h.hold(10, domain.DisabledGeoAutoSuspend, holdT)
	h.hold(15, domain.DisabledGeoAutoSuspend, holdT)
	h.flags.latest = map[int64]int64{
		11: testNow.Add(-5 * time.Minute).UnixMilli(),
		12: testNow.Add(-time.Minute).UnixMilli(),
		14: testNow.Add(-time.Minute).UnixMilli(),
		16: testNow.UnixMilli(),
	}

	v := h.queue(t, QueueQuery{})

	if got, want := rowIDs(v), []int64{10, 12, 14, 11, 13, 15, 16}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// The metric cards count the WHOLE queue whatever is filtered: 需立即处理
// (open and flagged or held), 已标记 and 可疑 (open, by level), 异地自动暂停
// (every held account, dismissed or not — one label for one state), 已忽略
// (in force) and 已信任.
func TestQueue_CountsIgnoreFilters(t *testing.T) {
	h := filterFleet(t)
	want := QueueCounts{Urgent: 3, Flagged: 2, Suspect: 1, AutoSuspended: 2, Dismissed: 2, Trusted: 1}
	for _, q := range []QueueQuery{{}, {Status: "dismissed"}, {Sources: []string{"geo"}, Q: "nobody"}, {Urgent: true, Page: 9}} {
		c := h.queue(t, q).Counts
		c.Online, c.OnlineTakenAt, c.OnlineStale, c.GeoUnknown = nil, time.Time{}, false, 0
		if !reflect.DeepEqual(c, want) {
			t.Fatalf("counts under %+v = %+v, want %+v", q, c, want)
		}
	}
}

// 在线 is the live snapshot's account count, with its time and staleness
// computed as the live view computes them; before the first snapshot it is
// unknown (nil), never 0 — "nobody online" and "never read" differ.
func TestQueue_OnlineFromSnapshotAndNullBeforeOne(t *testing.T) {
	h := newHarness()
	c := h.queue(t, QueueQuery{}).Counts
	if c.Online != nil || !c.OnlineTakenAt.IsZero() || !c.OnlineStale {
		t.Fatalf("before any snapshot: online %v at %v stale %v, want nil, zero, stale", c.Online, c.OnlineTakenAt, c.OnlineStale)
	}

	taken := testNow.Add(-time.Minute)
	h.live.snap = pollSnapshot(taken, conn(7, 1, "198.51.100.1", ""), conn(8, 1, "198.51.100.2", ""), conn(9, 2, "198.51.100.3", ""))
	c = h.queue(t, QueueQuery{}).Counts
	if c.Online == nil || *c.Online != 3 || !c.OnlineTakenAt.Equal(taken) || c.OnlineStale {
		t.Fatalf("online %v at %v stale %v, want 3 at %v, fresh", c.Online, c.OnlineTakenAt, c.OnlineStale, taken)
	}

	h.live.snap = pollSnapshot(testNow.Add(-time.Hour), conn(7, 1, "198.51.100.1", ""))
	if c = h.queue(t, QueueQuery{}).Counts; !c.OnlineStale || c.Online == nil || *c.Online != 1 {
		t.Fatalf("an hour-old snapshot: online %v stale %v, want 1 and stale", c.Online, c.OnlineStale)
	}
}

// An account deleted between the attention reads and the naming is never
// listed: the queue must not name somebody who no longer exists.
func TestQueue_DeletedAccountsNeverListed(t *testing.T) {
	h := newHarness()
	h.user(1, "alive")
	h.geo.rows = []domain.GeoRecord{geoAt(1, true, 0, domain.GeoStateFlagged, testNow), geoAt(99, true, 0, domain.GeoStateFlagged, testNow)}

	v := h.queue(t, QueueQuery{})

	if got := rowIDs(v); !slices.Equal(got, []int64{1}) || v.Total != 1 {
		t.Fatalf("rows %v of %d, want [1] of 1", got, v.Total)
	}
	if len(h.users.listed) != 1 || !slices.Equal(h.users.listed[0], []int64{1, 99}) {
		t.Fatalf("named %v, want one read of the filtered ids [1 99]", h.users.listed)
	}
}

// An unknown status, source or level is refused before anything is read —
// an ignored filter would answer for the whole fleet.
func TestQueue_RejectsUnknownFilters(t *testing.T) {
	h := newHarness()
	for _, q := range []QueueQuery{
		{Status: "pending"},
		{Sources: []string{"geo", "bogus"}},
		{Sources: []string{"review"}},
		{Level: domain.FlagLevelSuspended},
		{Level: "high"},
	} {
		if _, err := h.svc.Queue(t.Context(), q); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%+v = %v, want ErrValidation", q, err)
		}
	}
	if h.geo.attnCalled != 0 {
		t.Fatalf("a refused filter still read the stores (%d geo reads)", h.geo.attnCalled)
	}
}

// 最近变化 is the account's latest non-review flag record — what the
// account last did, not what an admin last did about it; with none, the
// time of a geo_auto hold; with neither, unknown (0). Asked once, for the
// filtered accounts only.
func TestQueue_ChangedAtIsLatestNonReviewRecordElseHoldTimeElseZero(t *testing.T) {
	h := newHarness()
	for id := int64(1); id <= 4; id++ {
		h.user(id, "u")
	}
	recordT, holdT := testNow.Add(-10*time.Minute), testNow.Add(-2*time.Hour)
	h.geo.rows = []domain.GeoRecord{geoAt(1, true, 0, domain.GeoStateFlagged, testNow), geoAt(3, false, 1, domain.GeoStateSuspect, testNow)}
	h.signals.rows = []domain.RiskSignal{signalAt(4, domain.RiskKindDevices, domain.GeoStateSuspect, testNow)}
	h.hold(1, domain.DisabledGeoAutoSuspend, holdT)
	h.hold(2, domain.DisabledGeoAutoSuspend, holdT)
	h.flags.latest = map[int64]int64{1: recordT.UnixMilli()}

	v := h.queue(t, QueueQuery{Sources: []string{"geo", "geo_auto"}})

	for uid, want := range map[int64]int64{1: recordT.UnixMilli(), 2: holdT.UnixMilli(), 3: 0} {
		if got := rowOf(t, v, uid).ChangedAtMS; got != want {
			t.Fatalf("account %d changed at %d, want %d", uid, got, want)
		}
	}
	if len(h.flags.latestAsked) != 1 {
		t.Fatalf("latest records read %d times, want once", len(h.flags.latestAsked))
	}
	asked := slices.Sorted(slices.Values(h.flags.latestAsked[0]))
	if !slices.Equal(asked, []int64{1, 2, 3}) {
		t.Fatalf("latest records asked for %v, want the filtered accounts [1 2 3] (not 4)", asked)
	}
}

// The evidence — the widest columns — is read for the PAGE only: the geo
// rows of the page's accounts with a geo source, the signals of those with
// a risk kind, and each only when the page has any.
func TestQueue_EvidenceLoadedForThePageOnly(t *testing.T) {
	h := newHarness()
	for id := int64(1); id <= 31; id++ {
		h.user(id, "u")
		if id <= 30 {
			h.geo.rows = append(h.geo.rows, geoAt(id, false, 1, domain.GeoStateSuspect, testNow))
		}
	}
	h.signals.rows = []domain.RiskSignal{signalAt(31, domain.RiskKindDevices, domain.GeoStateFlagged, testNow)}

	v := h.queue(t, QueueQuery{Page: 1, PageSize: 5})
	if got := rowIDs(v); !slices.Equal(got, []int64{31, 1, 2, 3, 4}) || v.Total != 31 || v.PageSize != 5 {
		t.Fatalf("page 1 = %v of %d (size %d), want [31 1 2 3 4] of 31", got, v.Total, v.PageSize)
	}
	if len(h.geo.listed) != 1 || !slices.Equal(h.geo.listed[0], []int64{1, 2, 3, 4}) {
		t.Fatalf("geo evidence read for %v, want [[1 2 3 4]]", h.geo.listed)
	}
	if len(h.signals.listed) != 1 || !slices.Equal(h.signals.listed[0], []int64{31}) {
		t.Fatalf("signal evidence read for %v, want [[31]]", h.signals.listed)
	}
	if rowOf(t, v, 31).Geo != nil || rowOf(t, v, 1).Geo == nil {
		t.Fatal("geo evidence must be on the rows with a geo source and only those")
	}
	if h.groups.calls != 1 {
		t.Fatalf("groups listed %d times, want once per page", h.groups.calls)
	}

	h.geo.listed, h.signals.listed = nil, nil
	v = h.queue(t, QueueQuery{Page: 2, PageSize: 5})
	if got := rowIDs(v); !slices.Equal(got, []int64{5, 6, 7, 8, 9}) {
		t.Fatalf("page 2 = %v, want [5 6 7 8 9]", got)
	}
	if len(h.signals.listed) != 0 || len(h.geo.listed) != 1 {
		t.Fatalf("page 2 read signals %v and geo %v, want no signal read (no kind on the page)", h.signals.listed, h.geo.listed)
	}

	if v = h.queue(t, QueueQuery{Page: 99, PageSize: 5}); len(v.Rows) != 0 || v.Total != 31 {
		t.Fatalf("past the last page = %v of %d, want none of 31", rowIDs(v), v.Total)
	}
	if v = h.queue(t, QueueQuery{PageSize: 1000}); v.PageSize != 100 || v.Page != 1 {
		t.Fatalf("page %d size %d, want the size capped at 100 and page 1", v.Page, v.PageSize)
	}
	if v = h.queue(t, QueueQuery{}); v.PageSize != 25 {
		t.Fatalf("default page size = %d, want 25", v.PageSize)
	}
}

// "Every detector is off" is said only when the GLOBAL settings turn every
// one of them off — the geo scope and the five risk kinds. A group may
// still override that, which the page's copy says.
func TestQueue_GlobalDetectorsOff(t *testing.T) {
	h := newHarness()
	off := ports.UISettings{GeoAnomalyScope: " OFF ", RiskSubSpreadOff: true, RiskDevicesOff: true, RiskUsageShiftOff: true, RiskLoginCountryOff: true, RiskDestBlockOff: true}
	h.settings.set = off
	if !h.queue(t, QueueQuery{}).GlobalDetectorsOff {
		t.Fatal("every detector off globally, want GlobalDetectorsOff")
	}
	for _, on := range []func(*ports.UISettings){
		func(s *ports.UISettings) { s.GeoAnomalyScope = "city" },
		func(s *ports.UISettings) { s.RiskSubSpreadOff = false },
		func(s *ports.UISettings) { s.RiskDevicesOff = false },
		func(s *ports.UISettings) { s.RiskUsageShiftOff = false },
		func(s *ports.UISettings) { s.RiskLoginCountryOff = false },
		func(s *ports.UISettings) { s.RiskDestBlockOff = false },
	} {
		set := off
		on(&set)
		h.settings.set = set
		if h.queue(t, QueueQuery{}).GlobalDetectorsOff {
			t.Fatalf("%+v has a detector on, want GlobalDetectorsOff false", set)
		}
	}
}

func TestQueue_DestinationBlocksStayVisibleUnderLocationTrust(t *testing.T) {
	h := newHarness()
	h.user(7, "destination-review")
	h.user(8, "destination-suspect")
	h.trust(7)
	h.geo.rows = []domain.GeoRecord{geoAt(7, true, 0, domain.GeoStateFlagged, testNow)}
	h.signals.rows = []domain.RiskSignal{
		signalAt(7, domain.RiskKindLoginCountry, domain.GeoStateFlagged, testNow),
		signalAt(7, domain.RiskKindDestBlock, domain.GeoStateFlagged, testNow),
		signalAt(8, domain.RiskKindDestBlock, domain.GeoStateSuspect, testNow),
	}
	v := h.queue(t, QueueQuery{Sources: []string{"dest_block"}})
	if v.Total != 2 || v.Counts.Urgent != 1 || v.Counts.Flagged != 1 || v.Counts.Suspect != 1 || v.Counts.AutoSuspended != 0 {
		t.Fatalf("destination queue counts = %+v, total=%d", v.Counts, v.Total)
	}
	r := rowOf(t, v, 7)
	if !reflect.DeepEqual(r.Attention.Levels, domain.AttentionLevels{"dest_block": domain.FlagLevelFlagged}) || len(r.Signals) != 1 || r.Signals[0].Kind != domain.RiskKindDestBlock || r.Geo != nil {
		t.Fatalf("trust hid destination or exposed location attention: %+v", r)
	}
	if r.User.ServiceDisabledReason != "" {
		t.Fatal("observe-only destination attention suspended an account")
	}
	h.signals.rows[1].UpdatedAtMS = testNow.Add(-48 * time.Hour).UnixMilli()
	if got := h.queue(t, QueueQuery{Urgent: true}).Total; got != 0 {
		t.Fatalf("stale destination signal remained urgent: %d", got)
	}
}

// The fresh geo verdicts nobody could place are counted over the same
// window as the geo attention, so a dead GeoIP database — which leaves the
// queue empty — is never silent.
func TestQueue_GeoUnknownCount(t *testing.T) {
	h := newHarness()
	h.geo.unknown = 4
	v := h.queue(t, QueueQuery{})
	if v.Counts.GeoUnknown != 4 {
		t.Fatalf("geo unknown = %d, want 4", v.Counts.GeoUnknown)
	}
	if !h.geo.unkSince.Equal(h.geo.gotSince) || h.geo.unkSince.IsZero() {
		t.Fatalf("unknown counted since %v, attention read since %v; want the same window", h.geo.unkSince, h.geo.gotSince)
	}
}

// A store that cannot answer fails the queue loudly: an empty queue must
// mean "nothing to look at", never "could not look".
func TestQueue_StoreErrorsFail(t *testing.T) {
	boom := errors.New("db gone")
	for name, set := range map[string]func(*harness){
		"geo":     func(h *harness) { h.geo.err = boom },
		"signals": func(h *harness) { h.signals.err = boom },
		"reviews": func(h *harness) { h.reviews.err = boom },
		"holds":   func(h *harness) { h.holds.err = boom },
		"users":   func(h *harness) { h.users.listErr = boom },
	} {
		h := newHarness()
		h.user(1, "u")
		h.geo.rows = []domain.GeoRecord{geoAt(1, true, 0, domain.GeoStateFlagged, testNow)}
		set(h)
		if _, err := h.svc.Queue(t.Context(), QueueQuery{}); !errors.Is(err, boom) {
			t.Fatalf("%s unreadable: err = %v, want it returned", name, err)
		}
	}
}

// The Users page's chips: every account at attention and every trusted one
// (a hold-only or trusted-only account has no level), each with whether it
// is open, dismissed in force, held and trusted.
func TestLevels_AttentionAndTrustedAccounts(t *testing.T) {
	h := newHarness()
	for id := int64(1); id <= 5; id++ {
		h.user(id, "u")
	}
	h.geo.rows = []domain.GeoRecord{geoAt(1, true, 0, domain.GeoStateFlagged, testNow), geoAt(5, false, 0, domain.GeoStateClean, testNow)}
	h.signals.rows = []domain.RiskSignal{signalAt(2, domain.RiskKindDevices, domain.GeoStateSuspect, testNow)}
	h.dismiss(2, testNow.Add(-time.Minute), domain.DismissSnapshot{"devices": accepted(domain.FlagLevelSuspect, testNow.Add(-2*time.Minute))})
	h.hold(3, domain.DisabledGeoAutoSuspend, testNow.Add(-time.Hour))
	h.trust(4)

	got, err := h.svc.Levels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]UserLevel{
		1: {Level: domain.FlagLevelFlagged, Open: true},
		2: {Level: domain.FlagLevelSuspect, Dismissed: true},
		3: {AutoSuspended: true, Open: true},
		4: {Trusted: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("levels = %+v, want %+v", got, want)
	}
}

// The bell's number: accounts open AND flagged or held. Suspect alone does
// not ring; a dismissal in force silences; a reopened or lapsed one rings
// again.
func TestCountUrgent_FlaggedOrHeldAndOpen(t *testing.T) {
	h := newHarness()
	h.settings.set = ports.UISettings{RiskFlagRecordRetentionDays: 1}
	for id := int64(1); id <= 6; id++ {
		h.user(id, "u")
	}
	verdict, d := testNow.Add(-2*time.Hour), testNow.Add(-time.Hour)
	h.geo.rows = []domain.GeoRecord{
		geoAt(1, true, 0, domain.GeoStateFlagged, testNow),
		geoAt(2, false, 1, domain.GeoStateSuspect, testNow),
		geoAt(4, true, 0, domain.GeoStateFlagged, testNow),
		geoAt(5, true, 0, domain.GeoStateFlagged, testNow),
		geoAt(6, true, 0, domain.GeoStateFlagged, testNow),
	}
	h.hold(3, domain.DisabledGeoAutoSuspend, testNow.Add(-time.Hour))
	h.dismiss(4, d, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, verdict)})
	h.dismiss(5, d, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelSuspect, verdict)}) // escalated since
	h.dismiss(6, testNow.Add(-72*time.Hour), domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, testNow.Add(-73*time.Hour))})

	n, err := h.svc.CountUrgent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("urgent = %d, want 4 (1 flagged, 3 held, 5 reopened, 6 lapsed)", n)
	}
}

// The drawer, the review actions and the queue must see the SAME account:
// UserAttention computes one account the way the queue computes the fleet
// — the same freshness bounds (inclusive), the same trust mask, the same
// reopen rule over the same records, the same hold.
func TestUserAttention_EqualsTheQueueComputation(t *testing.T) {
	h := newHarness()
	h.settings.set = ports.UISettings{RiskAlertFreshnessHours: 2, CronTrafficPullMinutes: 120, RiskFlagRecordRetentionDays: 1}
	for id := int64(1); id <= 9; id++ {
		h.user(id, "u")
	}
	geoSince, riskSince := testNow.Add(-4*time.Hour), testNow.Add(-2*time.Hour)
	verdict, d := testNow.Add(-90*time.Minute), testNow.Add(-time.Hour)
	h.geo.rows = []domain.GeoRecord{
		geoAt(1, true, 0, domain.GeoStateIdle, geoSince),                        // at the bound: in
		geoAt(2, true, 0, domain.GeoStateIdle, geoSince.Add(-time.Millisecond)), // out
		geoAt(3, true, 0, domain.GeoStateFlagged, testNow),                      // trusted: masked
		geoAt(4, false, 2, domain.GeoStateSuspect, testNow),                     // dismissed, then escalated
		geoAt(6, true, 0, domain.GeoStateFlagged, testNow),                      // lapsed
	}
	h.signals.rows = []domain.RiskSignal{
		signalAt(1, domain.RiskKindUsageShift, domain.GeoStateSuspect, riskSince),
		signalAt(3, domain.RiskKindDevices, domain.GeoStateFlagged, testNow),
		signalAt(5, domain.RiskKindLoginCountry, domain.GeoStateFlagged, riskSince.Add(-time.Millisecond)),
		signalAt(7, domain.RiskKindSubSpread, domain.GeoStateUnknown, testNow),
	}
	h.hold(8, domain.DisabledGeoAutoSuspend, testNow.Add(-30*time.Minute))
	h.trust(3)
	h.dismiss(4, d, domain.DismissSnapshot{"geo": accepted(domain.FlagLevelSuspect, verdict)})
	h.flags.steps = map[int64][]domain.FlagStep{4: {
		{Source: "geo", Level: domain.FlagLevelFlagged, State: domain.GeoStateFlagged, AtMS: d.Add(time.Minute).UnixMilli()},
		{Source: "geo", Level: domain.FlagLevelSuspect, State: domain.GeoStateSuspect, AtMS: d.Add(2 * time.Minute).UnixMilli()},
	}}
	h.dismiss(6, testNow.Add(-72*time.Hour), domain.DismissSnapshot{"geo": accepted(domain.FlagLevelFlagged, testNow.Add(-73*time.Hour))})
	h.trust(9)

	v := h.queue(t, QueueQuery{Status: "all", PageSize: 100})
	listed := map[int64]domain.AccountAttention{}
	for _, r := range v.Rows {
		listed[r.User.ID] = r.Attention
	}
	for _, uid := range []int64{1, 3, 4, 6, 8, 9} {
		if _, ok := listed[uid]; !ok {
			t.Fatalf("account %d not in the queue (%v)", uid, rowIDs(v))
		}
	}
	for uid := int64(1); uid <= 9; uid++ {
		got, err := h.svc.UserAttention(t.Context(), uid)
		if err != nil {
			t.Fatalf("account %d: %v", uid, err)
		}
		if !reflect.DeepEqual(got, listed[uid]) {
			t.Fatalf("account %d: UserAttention = %+v, the queue = %+v", uid, got, listed[uid])
		}
	}
	if !rowOf(t, v, 4).Attention.State.Reopened {
		t.Fatal("account 4's sticky escalation did not reopen it: the fixture tests nothing")
	}
	if _, err := h.svc.UserAttention(t.Context(), 404); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a missing account = %v, want ErrNotFound", err)
	}
}
