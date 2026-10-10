package metrics

import (
	"strings"
	"sync"
	"testing"
)

func TestCounterPairVecKeepsKindsAndOutcomesIndependent(t *testing.T) {
	v := NewCounterPairVec("test_pair_isolation", "pair test", "kind", "outcome")
	accepted, invalid, trial := v.With("block", "accepted"), v.With("block", "invalid"), v.With("trial", "accepted")
	if accepted == invalid || accepted == trial || accepted != v.With("block", "accepted") {
		t.Fatal("distinct kind/outcome pairs share a counter")
	}
	accepted.Add(3)
	invalid.Inc()
	trial.Add(7)
	if accepted.Value() != 3 || invalid.Value() != 1 || trial.Value() != 7 {
		t.Fatal("pair counts crossed labels")
	}
	want := map[string]int64{"test_pair_isolation{kind=block,outcome=accepted}": 3, "test_pair_isolation{kind=block,outcome=invalid}": 1, "test_pair_isolation{kind=trial,outcome=accepted}": 7}
	for _, c := range Take().Counters {
		if strings.HasPrefix(c.Name, "test_pair_isolation{") {
			n, ok := want[c.Name]
			if !ok || c.Value != n {
				t.Fatalf("pair snapshot name/count changed: %s %d", c.Name, c.Value)
			}
			delete(want, c.Name)
		}
	}
	if len(want) != 0 {
		t.Fatal("missing pair snapshot series")
	}
}

func TestCounterPairVecConcurrentFirstUseDoesNotLoseCounts(t *testing.T) {
	v := NewCounterPairVec("test_pair_concurrent", "pair test", "kind", "outcome")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			for n := 0; n < 100; n++ {
				v.With("block", "accepted").Inc()
				v.With("block", "invalid").Inc()
			}
		})
	}
	wg.Wait()
	if v.With("block", "accepted").Value() != 10000 || v.With("block", "invalid").Value() != 10000 {
		t.Fatal("concurrent pair counters lost or mixed observations")
	}
}

func TestCounterPairVecLeavesExistingSingleLabelValuesIntact(t *testing.T) {
	v := NewCounterVec("test_single_grammar", "single test", "reason")
	value := "phase=compile,outcome=old"
	v.With(value).Inc()
	for _, c := range Take().Counters {
		if c.Name == "test_single_grammar{reason="+value+"}" && c.Value == 1 {
			return
		}
	}
	t.Fatal("existing single label value was reinterpreted")
}
