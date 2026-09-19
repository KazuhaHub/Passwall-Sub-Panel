package auth

import (
	"testing"
	"time"

	"github.com/crewjam/saml"
)

// withConditions builds an assertion carrying only a Conditions window.
func withConditions(notOnOrAfter time.Time) *saml.Assertion {
	return &saml.Assertion{Conditions: &saml.Conditions{NotOnOrAfter: notOnOrAfter}}
}

// withConfirmations builds an assertion carrying only SubjectConfirmationData
// windows. A nil entry stands for a confirmation with no data element at all.
func withConfirmations(notOnOrAfter ...*time.Time) *saml.Assertion {
	scs := make([]saml.SubjectConfirmation, 0, len(notOnOrAfter))
	for _, at := range notOnOrAfter {
		sc := saml.SubjectConfirmation{}
		if at != nil {
			sc.SubjectConfirmationData = &saml.SubjectConfirmationData{NotOnOrAfter: *at}
		}
		scs = append(scs, sc)
	}
	return &saml.Assertion{Subject: &saml.Subject{SubjectConfirmations: scs}}
}

// The window has to cover whichever of the two windows crewjam would still accept
// the assertion in — it enforces Conditions AND every SubjectConfirmationData, so
// reading only one of them leaves a replay window open for the other.
func TestSAMLReplayExpiry(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	floor := now.Add(samlReplayFloor)
	stamp := func(t time.Time) time.Time {
		return t.Add(saml.MaxClockSkew).Add(samlReplayExpiryMargin)
	}

	both := func(conditions, confirmation time.Time) *saml.Assertion {
		a := withConditions(conditions)
		a.Subject = withConfirmations(ptrTime(confirmation)).Subject
		return a
	}

	cases := map[string]struct {
		assertion *saml.Assertion
		want      time.Time
	}{
		"nothing declared falls back to the floor": {
			assertion: &saml.Assertion{},
			want:      stamp(floor),
		},
		"conditions later than the floor": {
			assertion: withConditions(now.Add(20 * time.Minute)),
			want:      stamp(now.Add(20 * time.Minute)),
		},
		"conditions earlier than the floor keeps the floor": {
			assertion: withConditions(now.Add(time.Minute)),
			want:      stamp(floor),
		},
		"confirmation later than conditions wins": {
			// The branch the previous implementation missed: it read Conditions
			// alone, so this record would have expired while crewjam would still
			// have accepted a second presentation.
			assertion: both(now.Add(10*time.Minute), now.Add(30*time.Minute)),
			want:      stamp(now.Add(30 * time.Minute)),
		},
		"conditions later than confirmation wins": {
			assertion: both(now.Add(30*time.Minute), now.Add(10*time.Minute)),
			want:      stamp(now.Add(30 * time.Minute)),
		},
		"the latest of several confirmations wins": {
			assertion: withConfirmations(
				ptrTime(now.Add(10*time.Minute)), ptrTime(now.Add(40*time.Minute)), ptrTime(now.Add(20*time.Minute))),
			want: stamp(now.Add(40 * time.Minute)),
		},
		"zero-valued windows are ignored": {
			assertion: &saml.Assertion{
				Conditions: &saml.Conditions{},
				Subject: &saml.Subject{SubjectConfirmations: []saml.SubjectConfirmation{
					{SubjectConfirmationData: &saml.SubjectConfirmationData{}},
				}},
			},
			want: stamp(floor),
		},
		"a confirmation without data is skipped": {
			assertion: withConfirmations(nil, ptrTime(now.Add(15*time.Minute))),
			want:      stamp(now.Add(15 * time.Minute)),
		},
		"a subject without confirmations is not a nil dereference": {
			assertion: &saml.Assertion{Subject: &saml.Subject{}},
			want:      stamp(floor),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := samlReplayExpiry(tc.assertion, now)
			if !got.Equal(tc.want) {
				t.Fatalf("expiry = %s, want %s", got, tc.want)
			}
		})
	}
}

// The service's clock is the one the window is measured from, so a test can pin
// the behaviour at a boundary instead of only around "now".
func TestSAMLService_UsesTheInjectedClock(t *testing.T) {
	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	svc, _ := testSAML(t)
	svc.clockFn = func() time.Time { return fixed }

	if got := svc.now(); !got.Equal(fixed) {
		t.Fatalf("now() = %s, want the injected %s", got, fixed)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
