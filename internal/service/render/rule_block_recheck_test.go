package render

import (
	"context"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The first render of a context proves the reduced assembly for ONE subscriber.
// Every later subscriber of that context is then served the reduced document on
// the strength of an argument (each pass touches a shared rule only through its
// target), not of a check. So a proven context is checked again every
// reducedRecheckEvery renders, on whichever subscriber that render belongs to.

func countComparisons(s *Service) *int {
	n := 0
	s.ruleBlockCompared = func(bool) { n++ }
	return &n
}

func TestReducedRuleAssemblyIsComparedAgainEverySoManyRenders(t *testing.T) {
	sc := goldenScenario{shared: "- DOMAIN,a.example,国外媒体\n- DOMAIN,b.example,国外媒体\n- MATCH,国外媒体"}
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		s, u := sc.build(t)
		compared := countComparisons(s)
		render := func() {
			t.Helper()
			if _, err := s.RenderForUser(context.Background(), u, ct); err != nil {
				t.Fatal(err)
			}
		}
		render()
		if *compared != 1 {
			t.Fatalf("%s: the first render of a context is compared, got %d comparisons", ct, *compared)
		}
		for i := 0; i < reducedRecheckEvery-1; i++ {
			render()
		}
		if *compared != 1 {
			t.Fatalf("%s: %d renders after the proof must not compare again, got %d comparisons", ct, reducedRecheckEvery-1, *compared)
		}
		render()
		if *compared != 2 {
			t.Fatalf("%s: render %d after the proof must compare again, got %d comparisons", ct, reducedRecheckEvery, *compared)
		}
	}
}

// A difference found by a later check is handled like one found by the first:
// that render returns the full pipeline's document and the context keeps the
// full pipeline from then on.
func TestReducedRuleAssemblyThatDiffersOnALaterCheckIsPinnedToTheFullPipeline(t *testing.T) {
	sc := goldenScenario{shared: "- DOMAIN,a.example,国外媒体\n- DOMAIN,b.example,国外媒体\n- MATCH,国外媒体"}
	s, u := sc.build(t)
	want, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	// Proven on the first render above. Now the recorded block goes wrong in a
	// rule that is not a representative, which no per-render check can see.
	block := s.ruleBlocks.get(sc.shared, "  ", analyseRuleBlock)
	if block == nil {
		t.Fatal("the rule block was not analysed")
	}
	block.emitted = strings.Replace(block.emitted, "b.example", "c.example", 1)

	served := 0
	for i := 0; i < reducedRecheckEvery-1; i++ {
		got, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Body) != string(want.Body) {
			served++
		}
	}
	if served != reducedRecheckEvery-1 {
		t.Fatalf("the test's premise: between checks the wrong block is served (%d of %d renders)", served, reducedRecheckEvery-1)
	}
	report := observeReduced(s)
	for i := 0; i < 3; i++ {
		got, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Body) != string(want.Body) {
			t.Fatalf("render %d after the check still served the wrong document", i)
		}
	}
	if report() != "differs" {
		t.Fatalf("the check must have found the difference, got %q", report())
	}
}

// The operator's way out without a release: with the full pipeline forced, no
// render is reduced or compared, and the documents are the full pipeline's.
func TestFullPipelineOnlyNeverReduces(t *testing.T) {
	sc := goldenScenario{shared: "- DOMAIN,a.example,国外媒体\n- DOMAIN,b.example,国外媒体\n- MATCH,国外媒体"}
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		s, u := sc.build(t)
		want, err := s.RenderForUser(context.Background(), u, ct)
		if err != nil {
			t.Fatal(err)
		}
		s, u = sc.build(t)
		s.SetFullPipelineOnly(true)
		compared := countComparisons(s)
		for i := 0; i < 3; i++ {
			got, err := s.RenderForUser(context.Background(), u, ct)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Body) != string(want.Body) {
				t.Fatalf("%s: forced full pipeline rendered a different document", ct)
			}
		}
		if *compared != 0 {
			t.Fatalf("%s: forced full pipeline must not assemble the reduced document, got %d comparisons", ct, *compared)
		}
	}
}
