package rulematch

import "strings"

// Hit is one rule of the walked list that matched, or might have.
type Hit struct {
	Index  int // position in the entries passed to Walk
	Target string
	Reason Reason // set for undecided hits
}

// Route is the outcome of walking a rule list top to bottom for one probe.
type Route struct {
	// First is the first rule that decidably matches (PASS-targeted matches are
	// skipped, as Mihomo does). nil when none does.
	First *Hit
	// Final: First's target ends the matching (a built-in outbound, or a group
	// that cannot lead to PASS or a Rematch outbound). Meaningful when First != nil.
	Final bool
	// Maybe lists the undecided lines above First (all of them when First is
	// nil): any of them may match first.
	Maybe      []Hit
	MaybeTotal int
	// Later lists rules below First that also match. When First is final they
	// never apply; when it is not, they may.
	Later      []Hit
	LaterTotal int
	// Passed lists rules above First that matched but name PASS, which skips
	// them and carries on.
	Passed []Hit
}

// Cap bounds the lists in a Route; the totals keep counting.
const Cap = 100

// MayContinue says whether a match on target is not the last word: Mihomo
// follows a group into its current choice and carries on when that is PASS
// (tunnel/tunnel.go match), and over UDP when the chosen proxy cannot carry
// UDP. udp is true for a UDP probe.
type MayContinue func(target string, udp bool) bool

// Walk evaluates entries (rule text without the leading "- ") in order.
// Entries that are not rules or name no outbound send nothing anywhere and are
// skipped, except SUB-RULE, which names a sub-rule list instead of an outbound.
func Walk(entries []string, probe *Probe, cont MayContinue) Route {
	p := *probe
	p.resolvedEarlier = false
	var out Route
	for i, entry := range entries {
		r := ParseRule(entry)
		if r.Type == "" || (r.Target == "" && r.Type != "SUB-RULE") {
			continue
		}
		v := Eval(r, &p)
		// An address rule that does not say no-resolve makes Mihomo resolve a
		// domain when it is reached, match or not.
		if !p.IsIP && resolves(r) {
			p.resolvedEarlier = true
		}
		switch v.Verdict {
		case No:
			continue
		case Unknown:
			if out.First == nil {
				out.MaybeTotal++
				if len(out.Maybe) < Cap {
					out.Maybe = append(out.Maybe, Hit{Index: i, Target: r.Target, Reason: v.Reason})
				}
			}
			continue
		}
		hit := Hit{Index: i, Target: r.Target}
		if out.First == nil {
			if strings.EqualFold(r.Target, "PASS") {
				out.Passed = append(out.Passed, hit)
				continue
			}
			out.First = &hit
			out.Final = cont == nil || !cont(r.Target, p.Network == "udp")
		} else {
			out.LaterTotal++
			if len(out.Later) < Cap {
				out.Later = append(out.Later, hit)
			}
		}
	}
	return out
}
