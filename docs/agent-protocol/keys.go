// Package protocol is the wire contract between PSP and a Passwall-Node agent.
//
// It is the compilable form of docs/psp-node-agent.md §8. Every exported name
// here corresponds to a decision recorded there, and the doc comments say WHICH
// decision and why — so that changing a type is visibly changing a decision
// rather than tidying a struct.
//
// This package holds types and pure functions only. No HTTP, no I/O, no
// database: both sides depend on it, so anything it imports becomes a
// dependency of both.
package protocol

import (
	"fmt"
	"strconv"
	"strings"
)

// Keys are PSP-MINTED ROW IDS, never derived strings.
//
// §2.4 ① records this being got wrong twice with two different strings. The
// panel-wide-unique email was the first (it is a render product: the partition
// suffix is dropped when a panel needs only one client, so the key changes when
// the partition COUNT crosses 1↔2). partKey.canon() was the second, prescribed
// in this repo and retracted the same day: clientplan pairs the sorted
// password-class and flow requirements BY INDEX, so a client's canon changes
// when a DIFFERENT node appears or disappears.
//
// Both failures share a shape — a key that is a function of something other
// than the thing it identifies. A row id is a function of nothing but the row.
//
// Why it matters more here than in a database: the roster is a full membership
// replacement, so an old key's ABSENCE deletes the object, and a deleted
// client's counters restart at zero. A key that drifts is not a naming
// inconvenience, it is silent data loss.
const (
	listenerKeyPrefix = "lst_"
	clientKeyPrefix   = "cli_"
	subjectKeyPrefix  = "usr_"
)

// ListenerKey identifies one listener. Minted from PSP's nodes row id.
type ListenerKey string

// ClientKey identifies one credential-carrying client row. Minted from PSP's
// psp_clients row id.
//
// Distinct from SubjectKey as a TYPE, not just by convention: §8.4 rules that
// quota is addressed and epoch-tracked per CLIENT ROW and must not be promoted
// to the subject. One person can hold several client rows on one agent, and
// their counters advance independently; summing them into a subject-level datum
// makes the subtraction's member set change underneath it. Two types mean the
// compiler refuses the promotion rather than a reviewer having to catch it.
type ClientKey string

// SubjectKey identifies the PERSON several client rows may belong to. Minted
// from PSP's users row id.
//
// It exists for exactly one reason (§8.4): the agent needs it to sum bytes and
// union source IPs across one person's several partition clients locally. That
// removes the first of the two multiplications recorded in
// connection-limits.md §12.1 — per-email × per-panel.
type SubjectKey string

// NewListenerKey mints a listener key from a PSP node row id.
func NewListenerKey(nodeID int64) ListenerKey {
	return ListenerKey(listenerKeyPrefix + strconv.FormatInt(nodeID, 10))
}

// NewClientKey mints a client key from a PSP psp_clients row id.
func NewClientKey(clientID int64) ClientKey {
	return ClientKey(clientKeyPrefix + strconv.FormatInt(clientID, 10))
}

// NewSubjectKey mints a subject key from a PSP users row id.
func NewSubjectKey(userID int64) SubjectKey {
	return SubjectKey(subjectKeyPrefix + strconv.FormatInt(userID, 10))
}

// RowID recovers the PSP row id a key was minted from.
//
// The error is not decorative: an agent echoes keys back, and a key that does
// not parse means the agent is talking about something PSP never minted. That
// is a protocol violation to be reported, never a row id to guess at.
func (k ListenerKey) RowID() (int64, error) { return rowID(string(k), listenerKeyPrefix, "listener") }

// RowID recovers the PSP row id a key was minted from.
func (k ClientKey) RowID() (int64, error) { return rowID(string(k), clientKeyPrefix, "client") }

// RowID recovers the PSP row id a key was minted from.
func (k SubjectKey) RowID() (int64, error) { return rowID(string(k), subjectKeyPrefix, "subject") }

func rowID(s, prefix, kind string) (int64, error) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return 0, fmt.Errorf("%s key %q: want prefix %q", kind, s, prefix)
	}
	// Rejecting a leading "+"/"-"/zero-pad keeps the mapping one-to-one: two
	// spellings of one row id would let the same object hold two identities in
	// a membership set, and membership is what deletion is expressed with.
	if rest == "" || rest != strconv.FormatInt(parseOrZero(rest), 10) {
		return 0, fmt.Errorf("%s key %q: %q is not a canonical row id", kind, s, rest)
	}
	return parseOrZero(rest), nil
}

func parseOrZero(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
