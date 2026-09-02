package domain

import "sort"

// UserLiveIPs is one user's live source IPs across the WHOLE fleet, plus how
// much of the fleet the number actually covers.
//
// The coverage half is not optional detail. A user's credentials are spread
// over several panels, each of which counts only its own; the per-user total
// exists nowhere but here. If one panel could not be read, the total is an
// UNDERCOUNT, and an undercount displayed as a total is worse than no number:
// it reads as "this account is fine" precisely when the evidence is missing.
// Callers must render Unread before acting on Count.
type UserLiveIPs struct {
	UserID int64
	// IPs is the de-duplicated union across every panel that answered.
	// A person on two panels from one address appears once — the same
	// connection path, counted once, which is the whole point of
	// aggregating per user instead of per client email.
	IPs []string
	// Panels is the number of panels that contributed at least one IP for
	// this user. Not a health signal — a user idle on a panel legitimately
	// contributes nothing.
	Panels int
	// Unread is the number of panels whose live IPs could not be read at
	// all this cycle: an adapter that does not implement the read, or one
	// whose call failed. Above zero means Count is a floor, not a total.
	Unread int
}

// Count is the number this feature exists to produce: how many distinct
// source addresses this person is using right now, fleet-wide.
//
// It is an IP count, never a device count. The data plane sees connections
// and source addresses and has no device concept: a household behind one NAT
// reads as 1, one phone moving between wifi and cellular reads as 2. Naming
// it "devices" anywhere would promise something no layer here can deliver.
func (u UserLiveIPs) Count() int { return len(u.IPs) }

// Complete reports whether every panel was read, i.e. whether Count is a
// total rather than a floor.
func (u UserLiveIPs) Complete() bool { return u.Unread == 0 }

// PanelLiveIPs is one panel's answer: client email -> that email's live IPs.
// A nil map with Err set means the panel could not be read; the aggregator
// keeps those separate from "read fine, nobody online" rather than letting
// the two collapse into the same zero.
type PanelLiveIPs struct {
	PanelID int64
	ByEmail map[string][]string
	// Err marks a panel that could not be read this cycle — including one
	// whose adapter does not implement the read at all. Its users are
	// counted as Unread, never as zero.
	Err error
}

// clientKey identifies a client row the way the fleet does: an email is
// unique only WITHIN a panel, so the same email string on two panels is two
// different clients. Keying on email alone would silently merge them.
type clientKey struct {
	PanelID int64
	Email   string
}

// AggregateLiveIPsByUser folds per-panel, per-email observations into one
// row per user.
//
// owners maps each (panel, email) to the user who owns it — PSP's mapping,
// which no panel has. Everything else is arithmetic; the value is entirely
// in doing it at the only layer that can.
//
// An email with no owner is SKIPPED rather than counted under a synthetic
// user. Those are real: a client an operator created by hand on the panel,
// or one PSP has already released. Attributing them to anyone would inflate
// that person's count, and this number is going to be used to judge whether
// somebody is sharing an account.
//
// Every user in owners gets a row, including users with no live IPs at all.
// A caller asking "who is over their cap" needs the zeroes to distinguish
// "idle" from "not looked at", and a caller building a distribution needs
// them or the histogram is conditioned on being online.
func AggregateLiveIPsByUser(panels []PanelLiveIPs, owners map[clientKey]int64) map[int64]UserLiveIPs {
	// Seed every known user so absence is representable.
	acc := map[int64]map[string]struct{}{}
	panelsSeen := map[int64]map[int64]struct{}{}
	for _, uid := range owners {
		if acc[uid] == nil {
			acc[uid] = map[string]struct{}{}
			panelsSeen[uid] = map[int64]struct{}{}
		}
	}

	// A panel that could not be read makes every user who has a client on
	// it incomplete — not just the users who would have shown up in its
	// answer, which is unknowable precisely because it did not answer.
	unread := map[int64]int{}
	for _, p := range panels {
		if p.Err == nil {
			continue
		}
		for k, uid := range owners {
			if k.PanelID == p.PanelID {
				unread[uid]++
			}
		}
	}

	for _, p := range panels {
		if p.Err != nil {
			continue
		}
		for email, ips := range p.ByEmail {
			uid, ok := owners[clientKey{PanelID: p.PanelID, Email: email}]
			if !ok {
				continue
			}
			for _, ip := range ips {
				if ip == "" {
					continue
				}
				acc[uid][ip] = struct{}{}
				panelsSeen[uid][p.PanelID] = struct{}{}
			}
		}
	}

	out := make(map[int64]UserLiveIPs, len(acc))
	for uid, set := range acc {
		ips := make([]string, 0, len(set))
		for ip := range set {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
		out[uid] = UserLiveIPs{
			UserID: uid,
			IPs:    ips,
			Panels: len(panelsSeen[uid]),
			Unread: unread[uid],
		}
	}
	return out
}

// NewClientKey builds the aggregator's owner-map key. Exported so callers
// outside this package can assemble owners without re-deriving the rule that
// an email is unique per panel, not globally.
func NewClientKey(panelID int64, email string) clientKey {
	return clientKey{PanelID: panelID, Email: email}
}
