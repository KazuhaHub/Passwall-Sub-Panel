package domain

import (
	"testing"
	"time"
)

func TestClassifyProbe(t *testing.T) {
	for _, tc := range []struct {
		name               string
		attempts, failures int
		want               ProbeHealth
	}{
		{"no attempt", 0, 0, ProbeUnknown},
		{"clean", 20, 0, ProbeOK},
		{"under 20% failing", 20, 3, ProbeOK},
		{"exactly 20% failing", 20, 4, ProbeDegraded},
		{"mostly failing", 20, 19, ProbeDegraded},
		{"all failing", 20, 20, ProbeDown},
		{"three of three failing", 3, 3, ProbeDown},
		{"too few attempts to be down", 2, 2, ProbeDegraded},
		{"one attempt failing", 1, 1, ProbeDegraded},
		{"one attempt fine", 1, 0, ProbeOK},
	} {
		if got := ClassifyProbe(tc.attempts, tc.failures); got != tc.want {
			t.Errorf("%s: ClassifyProbe(%d, %d) = %s, want %s", tc.name, tc.attempts, tc.failures, got, tc.want)
		}
	}
}

// run feeds a sequence of windows through the hysteresis and returns the
// withheld flag after each.
func run(h ProbeHysteresis, windows ...ProbeHealth) []bool {
	var prev *ProbeState
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var out []bool
	for _, window := range windows {
		state, consecutive, since, withheld := h.Advance(prev, window, now)
		prev = &ProbeState{State: state, Consecutive: consecutive, StateSince: since, Withheld: withheld}
		out = append(out, withheld)
		now = now.Add(time.Minute)
	}
	return out
}

func TestProbeHysteresis(t *testing.T) {
	on := ProbeHysteresis{Withhold: true, DownAfter: 3, OKAfter: 2}
	d, o, g, u := ProbeDown, ProbeOK, ProbeDegraded, ProbeUnknown
	for _, tc := range []struct {
		name    string
		h       ProbeHysteresis
		windows []ProbeHealth
		want    []bool
	}{
		{"needs N consecutive down", on, []ProbeHealth{d, d, d, d}, []bool{false, false, true, true}},
		{"a break resets the down run", on, []ProbeHealth{d, d, o, d, d}, []bool{false, false, false, false, false}},
		{"degraded breaks a down run", on, []ProbeHealth{d, d, g, d}, []bool{false, false, false, false}},
		{"unknown breaks a down run and never sets", on, []ProbeHealth{d, d, u, d, u, u, u}, []bool{false, false, false, false, false, false, false}},
		{"unknown clears a flag", on, []ProbeHealth{d, d, d, u, d}, []bool{false, false, true, false, false}},
		{"restore needs M consecutive ok", on, []ProbeHealth{d, d, d, o, d, d, d, o, o}, []bool{false, false, true, true, true, true, true, true, false}},
		{"degraded and unknown do not restore", on, []ProbeHealth{d, d, d, g, g, o}, []bool{false, false, true, true, true, true}},
		{"first window counts as one", ProbeHysteresis{Withhold: true, DownAfter: 1, OKAfter: 1}, []ProbeHealth{d, o}, []bool{true, false}},
		{"withholding off never sets", ProbeHysteresis{DownAfter: 1, OKAfter: 1}, []ProbeHealth{d, d, d, d}, []bool{false, false, false, false}},
	} {
		got := run(tc.h, tc.windows...)
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("%s: withheld after window %d = %v, want %v (all: %v)", tc.name, i+1, got[i], tc.want[i], got)
				break
			}
		}
	}
}

// With withholding off the flag is cleared, not left: no race with the
// settings write can leave an object withheld.
func TestProbeHysteresisClearsTheFlagWhenOff(t *testing.T) {
	off := ProbeHysteresis{DownAfter: 1, OKAfter: 1}
	for _, window := range []ProbeHealth{ProbeDown, ProbeOK, ProbeDegraded, ProbeUnknown} {
		_, _, _, withheld := off.Advance(&ProbeState{State: ProbeDown, Consecutive: 5, Withheld: true}, window, time.Now())
		if withheld {
			t.Fatalf("a %s window left the flag set while withholding is off", window)
		}
	}
}

// A previous row that went stale is forgotten: its run and its flag do not
// carry across a gap, so a returning agent is judged on fresh windows only.
func TestProbeHysteresisForgetsAStaleRow(t *testing.T) {
	h := ProbeHysteresis{Withhold: true, DownAfter: 3, OKAfter: 3}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	stale := &ProbeState{State: ProbeDown, Consecutive: 9, Withheld: true, StateSince: now.Add(-time.Hour), ValidUntil: now.Add(-time.Minute)}
	state, n, since, withheld := h.Advance(stale, ProbeDown, now)
	if state != ProbeDown || n != 1 || !since.Equal(now) || withheld {
		t.Fatalf("after a gap: %s run %d since %v withheld %v, want a fresh run of 1 and no flag", state, n, since, withheld)
	}
	fresh := *stale
	fresh.ValidUntil = now.Add(time.Minute)
	if _, n, _, withheld := h.Advance(&fresh, ProbeDown, now); n != 10 || !withheld {
		t.Fatalf("a fresh row lost its run: %d %v", n, withheld)
	}
}

func TestSameWindow(t *testing.T) {
	a := ProbeState{Attempts: 20, Failures: 2, RTTMinMS: 1, RTTMedianMS: 2, RTTMaxMS: 3, LastError: "timeout", LastSuccessMS: 5}
	b := a
	b.ReportedAt = time.Now() // when the panel took it is not the measurement
	if !a.SameWindow(b) {
		t.Fatal("the same measurement differs")
	}
	for _, change := range []func(*ProbeState){
		func(p *ProbeState) { p.Attempts++ }, func(p *ProbeState) { p.Failures++ }, func(p *ProbeState) { p.RTTMinMS++ },
		func(p *ProbeState) { p.RTTMedianMS++ }, func(p *ProbeState) { p.RTTMaxMS++ },
		func(p *ProbeState) { p.LastError = "tls" }, func(p *ProbeState) { p.LastSuccessMS++ },
	} {
		c := a
		change(&c)
		if a.SameWindow(c) {
			t.Fatalf("a changed window is the same: %+v", c)
		}
	}
}

func TestProbeHysteresisRunBookkeeping(t *testing.T) {
	h := ProbeHysteresis{Withhold: true, DownAfter: 3, OKAfter: 3}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state, n, since, _ := h.Advance(nil, ProbeOK, t0)
	if state != ProbeOK || n != 1 || !since.Equal(t0) {
		t.Fatalf("first window: %s %d %v", state, n, since)
	}
	_, n, since, _ = h.Advance(&ProbeState{State: ProbeOK, Consecutive: 4, StateSince: t0}, ProbeOK, t0.Add(time.Hour))
	if n != 5 || !since.Equal(t0) {
		t.Fatalf("same state: %d %v, want 5 since %v", n, since, t0)
	}
	_, n, since, _ = h.Advance(&ProbeState{State: ProbeOK, Consecutive: 4, StateSince: t0}, ProbeDown, t0.Add(time.Hour))
	if n != 1 || !since.Equal(t0.Add(time.Hour)) {
		t.Fatalf("new state: %d %v", n, since)
	}
}

func TestProbeStateStalenessAndWithholding(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fresh := ProbeState{State: ProbeDown, Withheld: true, ValidUntil: now.Add(time.Minute)}
	if !fresh.WithheldAt(now) || fresh.HealthAt(now) != ProbeDown {
		t.Fatal("a fresh down row with the flag must withhold")
	}
	stale := fresh
	stale.ValidUntil = now
	if stale.WithheldAt(now) || stale.HealthAt(now) != ProbeUnknown {
		t.Fatal("a stale row must be unknown and must not withhold")
	}
	unknown := fresh
	unknown.State = ProbeUnknown
	if unknown.WithheldAt(now) {
		t.Fatal("an unknown row must not withhold, whatever flag it carries")
	}
}

func TestQualityOf(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	valid := now.Add(time.Hour)
	e2e := ProbeState{ObjectKey: "lnk_1", Kind: ProbeKindE2E, Attempts: 20, Failures: 2, RTTMedianMS: 120, State: ProbeOK, ValidUntil: valid, ReportedAt: now}
	transport := ProbeState{ObjectKey: "lnk_1", Kind: ProbeKindTransport, Attempts: 20, Failures: 20, State: ProbeDown, Withheld: true, ValidUntil: valid}
	q := QualityOf([]ProbeState{transport, e2e}, now)
	if q.State != ProbeDown || q.MedianRTTMS != 120 || q.LossPercent != 10 || !q.Withheld || q.Kind != ProbeKindE2E {
		t.Fatalf("quality = %+v: numbers come from e2e, the state is the worst, any withheld row withholds", q)
	}
	if q := QualityOf(nil, now); q.State != ProbeUnknown || q.Withheld {
		t.Fatalf("no rows = %+v", q)
	}
	e2e.ValidUntil = now.Add(-time.Second)
	if q := QualityOf([]ProbeState{e2e}, now); q.State != ProbeUnknown || q.MedianRTTMS != 0 || q.LossPercent != 0 {
		t.Fatalf("stale quality shows numbers: %+v", q)
	}
}

func TestPublishableNeedsNoProbeWithholding(t *testing.T) {
	line := RelayLine{Enabled: true, EntryPanelID: 3, ManagedReady: true}
	if !line.Publishable() {
		t.Fatal("a ready managed line is not publishable")
	}
	line.ProbeWithheld = true
	if line.Publishable() {
		t.Fatal("a probe-withheld line is publishable")
	}
	external := RelayLine{Enabled: true, ProbeWithheld: true}
	if external.Publishable() {
		t.Fatal("ProbeWithheld is ignored for an external line")
	}
}
