package domain

import (
	"math/rand"
	"reflect"
	"testing"
)

func keyed(key int) LineSlot   { return LineSlot{Key: key, Keyed: true} }
func floater(key int) LineSlot { return LineSlot{Key: key, Keyed: true, Floating: true} }
func identity(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// With nothing floating the stream is returned as is, whatever the keys look
// like. This is the compatibility guarantee at the lowest level.
func TestPlaceLinesNoPositionsIsIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 200; round++ {
		slots := make([]LineSlot, rng.Intn(20))
		for i := range slots {
			switch rng.Intn(3) {
			case 0:
				slots[i] = LineSlot{} // keyless: a positional group separator
			default:
				slots[i] = keyed(rng.Intn(5) * 10) // ties and non-monotonic keys on purpose
			}
		}
		if got := PlaceFloatingLines(slots); !reflect.DeepEqual(got, identity(len(slots))) {
			t.Fatalf("round %d: %v is not the identity for %+v", round, got, slots)
		}
	}
}

func TestPlaceLinesFloatingBetweenNodes(t *testing.T) {
	// A(10) a1(10, attached) B(20) b1(20, attached) b2(25, floating)... b2 is
	// declared under B but was dragged up between A's lines and B.
	slots := []LineSlot{keyed(10), keyed(10), keyed(20), keyed(20), floater(15)}
	want := []int{0, 1, 4, 2, 3}
	if got := PlaceFloatingLines(slots); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPlaceLinesFloatingAboveOwnLanding(t *testing.T) {
	slots := []LineSlot{keyed(10), keyed(20), floater(5)}
	want := []int{2, 0, 1}
	if got := PlaceFloatingLines(slots); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPlaceLinesEqualKeyGoesAfterTheItemAndItsAttachedLines(t *testing.T) {
	slots := []LineSlot{keyed(10), keyed(10), keyed(20), floater(10)}
	want := []int{0, 1, 3, 2}
	if got := PlaceFloatingLines(slots); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Floating lines that share an anchor keep key order, then original order (the
// landing order in the stream, then declaration order).
func TestPlaceLinesTieOrder(t *testing.T) {
	slots := []LineSlot{keyed(10), keyed(40), floater(25), floater(15), floater(25), floater(15)}
	want := []int{0, 3, 5, 2, 4, 1}
	if got := PlaceFloatingLines(slots); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// A keyless item (a positional group separator) never anchors a line, and a
// line placed right after the item before it stays above it.
func TestPlaceLinesRelativeToPositionalSeparator(t *testing.T) {
	// A(10) [positional separator] B(20); a line at 12 sits right after A, above
	// the separator; a line at 25 goes after B, below it.
	slots := []LineSlot{keyed(10), {}, keyed(20), floater(12), floater(25)}
	want := []int{0, 3, 1, 2, 4}
	if got := PlaceFloatingLines(slots); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// No anchor at all: only keyless items before it. It goes to the very start.
func TestPlaceLinesWithNoAnchorGoesFirst(t *testing.T) {
	slots := []LineSlot{{}, keyed(30), floater(5)}
	want := []int{2, 0, 1}
	if got := PlaceFloatingLines(slots); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// The result is always a permutation: nothing dropped, nothing duplicated.
func TestPlaceLinesIsAPermutation(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for round := 0; round < 300; round++ {
		slots := make([]LineSlot, rng.Intn(25))
		for i := range slots {
			k := rng.Intn(8) * 10
			switch rng.Intn(4) {
			case 0:
				slots[i] = LineSlot{}
			case 1:
				slots[i] = floater(k)
			default:
				slots[i] = keyed(k)
			}
		}
		got := PlaceFloatingLines(slots)
		seen := map[int]bool{}
		for _, idx := range got {
			if idx < 0 || idx >= len(slots) || seen[idx] {
				t.Fatalf("round %d: %v is not a permutation of %d", round, got, len(slots))
			}
			seen[idx] = true
		}
		if len(got) != len(slots) {
			t.Fatalf("round %d: lost entries: %v", round, got)
		}
	}
}
