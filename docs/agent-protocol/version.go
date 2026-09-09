package protocol

import "fmt"

// Version orders the states of one stream. It is a PAIR, not an int64.
//
// §8.3: a single monotonic counter has no way back. Restore PSP from a backup
// (or rebuild the agent's document row, or re-register the node) and the
// counter returns to 1, while the agent has 412 persisted. Under "reject
// anything not newer than what I applied" the agent then rejects every version
// PSP will ever mint again, serves the stale config indefinitely, and the only
// remedy is reinstalling it. A database restore is routine operations, not an
// edge case.
//
// Epoch is minted at registration and incremented whenever PSP REBUILDS the
// document row. On a higher epoch the agent clears its applied version and
// accepts what it is given — the way back that a bare counter does not have.
type Version struct {
	Epoch   uint64 `json:"epoch"`
	Version uint64 `json:"version"`
}

// Newer reports whether v is lexicographically after other.
//
// Comparison is only ever between the agent's applied version and PSP's minted
// one, and the agent's can never lead. So although the product of two
// independently monotonic counters is a PARTIAL order in general, every
// comparison this protocol actually makes is decidable. §8.2 says "comparable
// per stream" rather than "totally ordered" for exactly that reason: the weaker
// claim is the true one.
func (v Version) Newer(other Version) bool {
	if v.Epoch != other.Epoch {
		return v.Epoch > other.Epoch
	}
	return v.Version > other.Version
}

// Zero reports whether v has never been set.
func (v Version) Zero() bool { return v.Epoch == 0 && v.Version == 0 }

func (v Version) String() string { return fmt.Sprintf("%d:%d", v.Epoch, v.Version) }

// ETag is the content digest of a segment: the hex sha256 of its canonical
// serialization, and NOTHING else.
//
// §8.3 forbids putting the version in it, and the reason is mechanical rather
// than aesthetic. PSP's desired roster is computed in several places; any
// "recompute and write back the same value" pass mints a new version. If the
// version were part of the validator, that no-op would change the ETag, the
// agent would re-fetch a byte-identical document, and the steady-state
// zero-payload property would fail on precisely the churniest path. A
// timestamp fails the same way, which is why §7.2's rule names both.
//
// The mirror of that rule: minting must be CONTENT-IDEMPOTENT. Compare
// canonical bytes before assigning a version; identical content mints nothing.
type ETag string

// Convergence is judged by ETAG equality, never version equality (§8.3).
//
// Versions can revisit content — v5 and v7 may both be state A with v6 being B.
// After a rollback the agent holds A at v5 while PSP is at v7, and comparing
// versions reports drift that does not exist. Comparing content reports none,
// which is the truth.
func Converged(applied, want ETag) bool { return applied != "" && applied == want }
