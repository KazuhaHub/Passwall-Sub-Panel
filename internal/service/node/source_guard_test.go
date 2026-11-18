package node

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func guardService(repo *factorNodes, canGuard bool) *Service {
	svc := &Service{nodes: repo}
	svc.SetSourceGuardCheck(func(context.Context, int64) bool { return canGuard })
	return svc
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var coded *domain.CodedValidationError
	if !errors.As(err, &coded) || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want a coded validation error", err)
	}
	return coded.Code
}

func TestSourceGuardRefusesDifferingFactorsOnALandingThatCannotGuard(t *testing.T) {
	repo := &factorNodes{nodes: map[int64]*domain.Node{
		1: {ID: 1, PanelID: 10, DisplayName: "hk", Relays: []domain.RelayLine{{ID: "r", Name: "r", Address: "r.example", Enabled: true}}},
	}}
	svc := guardService(repo, false)
	ctx := context.Background()

	// A differing line factor, by the bulk editor and by the full editor.
	_, err := svc.SetLineFactors(ctx, 10, []LineFactorChange{{Line: domain.LineKey{NodeID: 1, LineID: "r"}, FactorMilli: 2000}})
	if codeOf(t, err) != CodeSourceGuardFactorMismatch || len(repo.written) != 0 {
		t.Fatalf("bulk: %v (written %d)", err, len(repo.written))
	}
	_, err = svc.SetLineFactors(ctx, 10, []LineFactorChange{{Line: domain.DirectLine(1), FactorMilli: 2000}})
	if codeOf(t, err) != CodeSourceGuardFactorMismatch {
		t.Fatalf("a direct factor that now differs from the line: %v", err)
	}
	// Moving both together is fine.
	if _, err := svc.SetLineFactors(ctx, 10, []LineFactorChange{
		{Line: domain.DirectLine(1), FactorMilli: 2000}, {Line: domain.LineKey{NodeID: 1, LineID: "r"}, FactorMilli: 2000},
	}); err != nil {
		t.Fatalf("equal factors: %v", err)
	}

	// A tunnel line has a listener of its own and is exempt.
	repo.nodes[1].Relays = []domain.RelayLine{{ID: "t", Mode: domain.RelayModeTunnel, EntryPanelID: 3, TrafficFactorMilli: milli(3000)}}
	repo.nodes[1].TrafficFactorMilli = nil
	if err := checkOnly(svc, repo.nodes[1]); err != nil {
		t.Fatalf("tunnel line: %v", err)
	}
}

func checkOnly(svc *Service, n *domain.Node) error {
	prev := *n
	prev.Relays = nil
	return svc.checkSourceGuard(context.Background(), &prev, n)
}

func TestSourceGuardNeedsAddressesForADifferingExternalLineOnALandingThatCanGuard(t *testing.T) {
	svc := guardService(&factorNodes{}, true)
	two := milli(2000)
	landing := func(lines ...domain.RelayLine) *domain.Node {
		return &domain.Node{ID: 1, PanelID: 10, DisplayName: "hk", Relays: lines}
	}
	prev := landing()
	external := domain.RelayLine{ID: "e", Name: "e", Address: "e.example", TrafficFactorMilli: two}
	if codeOf(t, svc.checkSourceGuard(context.Background(), prev, landing(external))) != CodeSourceGuardNeedsCIDRs {
		t.Fatal("an external line at another price without addresses was accepted")
	}
	external.SourceCIDRs = []string{"192.0.2.0/24"}
	for name, line := range map[string]domain.RelayLine{
		"with addresses":  external,
		"managed forward": {ID: "m", Address: "m.example", EntryPanelID: 7, TrafficFactorMilli: two},
		"cdn":             {ID: "c", Mode: domain.RelayModeCDN, TrafficFactorMilli: two},
		"same factor":     {ID: "s", Address: "s.example"},
		"tunnel":          {ID: "t", Mode: domain.RelayModeTunnel, EntryPanelID: 7, TrafficFactorMilli: two},
	} {
		if err := svc.checkSourceGuard(context.Background(), prev, landing(line)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// What was already wrong before a save does not stop an unrelated edit; only a
// line the save introduces or changes is refused.
func TestSourceGuardDoesNotBlockUnrelatedEditsOfAnAlreadyMismatchedLanding(t *testing.T) {
	svc := guardService(&factorNodes{}, false)
	mismatched := func(name string, factor *int) *domain.Node {
		return &domain.Node{ID: 1, PanelID: 10, DisplayName: name, Relays: []domain.RelayLine{{ID: "r", Name: "r", Address: "r", TrafficFactorMilli: factor}}}
	}
	before := mismatched("hk", milli(2000))
	if err := svc.checkSourceGuard(context.Background(), before, mismatched("renamed", milli(2000))); err != nil {
		t.Fatalf("a rename of a node that was already mismatched: %v", err)
	}
	if err := svc.checkSourceGuard(context.Background(), before, mismatched("hk", milli(3000))); codeOf(t, err) != CodeSourceGuardFactorMismatch {
		t.Fatalf("a changed factor: %v", err)
	}
}

func TestResolveSourceCIDRs(t *testing.T) {
	previous := &domain.Node{Relays: []domain.RelayLine{{ID: "a", SourceCIDRs: []string{"192.0.2.0/24"}}}}
	next := &domain.Node{Relays: []domain.RelayLine{
		{ID: "a", SourceCIDRsUnresolved: true},
		{ID: "b", SourceCIDRs: []string{"203.0.113.9", " 10.0.0.0/8 "}},
		{ID: "c", Mode: domain.RelayModeTunnel, SourceCIDRs: []string{"192.0.2.1"}},
	}}
	if err := resolveSourceCIDRs(previous, next); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Relays[0].SourceCIDRs, []string{"192.0.2.0/24"}) ||
		!reflect.DeepEqual(next.Relays[1].SourceCIDRs, []string{"10.0.0.0/8", "203.0.113.9/32"}) ||
		next.Relays[2].SourceCIDRs != nil {
		t.Fatalf("relays = %+v", next.Relays)
	}
	bad := &domain.Node{Relays: []domain.RelayLine{{ID: "x", SourceCIDRs: []string{"10.0.0.1/8"}}}}
	if err := resolveSourceCIDRs(nil, bad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("host bits: %v", err)
	}
	// An explicit empty list clears.
	clear := &domain.Node{Relays: []domain.RelayLine{{ID: "a", SourceCIDRs: []string{}}}}
	if err := resolveSourceCIDRs(previous, clear); err != nil || len(clear.Relays[0].SourceCIDRs) != 0 {
		t.Fatalf("clear: %v %+v", err, clear.Relays)
	}
}
