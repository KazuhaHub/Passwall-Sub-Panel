package traffic

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// TrustedLister lists the accounts an admin trusts (risk_reviews): their
// location is not judged. Read once per judging step — one poll's whole
// fleet shares one answer, and one narrow read per poll costs nothing where
// one per account would be a round trip per connected user.
type TrustedLister interface {
	ListTrusted(ctx context.Context) ([]int64, error)
}

// SetTrustedLister late-binds the trusted accounts, like the other detector
// dependencies. Nil is a supported state: nobody is trusted, and every
// account is judged as it was before trust existed.
func (s *Service) SetTrustedLister(l TrustedLister) { s.trusted = l }

// trustedSet is the accounts the next judging step treats as trusted: nil
// when no lister is wired, else the set just read.
//
// A failed read answers with the last set read successfully (nil before the
// first), not with nobody. Judged untrusted for one poll, every trusted
// account in two places would write a suspect or flagged verdict and its flag
// record, and the next good read the leave — attention changes that never
// happened, in a history an admin reads to decide. Holding the last answer
// can only delay an untrust by the outage, and a stale set cannot suspend
// anyone either way: the user repo refuses a geo_auto suspension of a
// trusted account inside its own conditional write, and a just-untrusted one
// starts again from a reset streak.
//
// Races with the review actions (the poll never takes their lock, and trust
// never takes geoJudgeMu):
//
//   - a trust that commits after this read and before the step judges: the
//     poll judges the account untrusted once more — possibly one more
//     suspect or flagged record — and the next poll records the leave. A ban
//     this poll finds due cannot land (the repo's guard), and the risk
//     center's reads mask a trusted account's location sources meanwhile
//     (domain.MaskTrusted);
//   - an untrust in the same gap: the account is judged trusted once more,
//     and judged from a reset streak on the next poll.
//
// The Warn carries counts only, never an account id: which accounts are
// trusted is an admin's decision about people, not an operator's log line.
func (s *Service) trustedSet(ctx context.Context) map[int64]bool {
	if s.trusted == nil {
		return nil
	}
	ids, err := s.trusted.ListTrusted(ctx)
	if err != nil {
		s.trustedMu.Lock()
		last := s.trustedLast
		s.trustedMu.Unlock()
		log.Warn("geo trust: could not read the trusted accounts; this poll judges with the last set read",
			"last_trusted", len(last), "err", err)
		return last
	}
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	// Replaced whole, never mutated after, so the lock is held only for the
	// swap: a step still judging with the previous map keeps reading a map
	// nobody writes.
	s.trustedMu.Lock()
	s.trustedLast = set
	s.trustedMu.Unlock()
	return set
}
