package domain

// DestResolve is the admin's per-panel choice of how a native node's routing
// treats a hostname against the IP rules (CIDRs, private addresses) of the
// destination policy. It is stored on the panel row and written only through
// the panel repo's narrow native-metadata writer, never by a whole-row save.
//
// It is per NODE and not per policy because Xray's domainStrategy is one value
// for the whole router, and because the DNS cost and the resolver are properties
// of the node.
type DestResolve string

const (
	// DestResolveAsIs does no DNS when routing: IP rules judge a connection made
	// to an address and never a hostname. The default.
	DestResolveAsIs DestResolve = "as_is"
	// DestResolveOnDemand resolves the hostname the first time a rule asks for
	// addresses and tries every IP rule on that set.
	DestResolveOnDemand DestResolve = "on_demand"
	// DestResolveIfNonMatch is Xray's IPIfNonMatch. It is NOT a value of
	// DestResolve (Valid is false): it reorders rules and lets report-only
	// change a verdict, so it is shown disabled and refused by the API. The
	// constant exists so the refusal can name it.
	DestResolveIfNonMatch DestResolve = "if_non_match"
)

// Valid reports whether r is a mode a panel can be set to.
func (r DestResolve) Valid() bool { return r == DestResolveAsIs || r == DestResolveOnDemand }

// NormalizeDestResolve resolves what a row or a request holds. Empty is the
// default (as_is), which is what a row created before the column existed reads
// as; anything that is not a mode reads as as_is too, because DNS at routing
// time is a cost and junk must not switch it on.
func NormalizeDestResolve(r DestResolve) DestResolve {
	if r == DestResolveOnDemand {
		return DestResolveOnDemand
	}
	return DestResolveAsIs
}

// Normalized is NormalizeDestResolve of r.
func (r DestResolve) Normalized() DestResolve { return NormalizeDestResolve(r) }
