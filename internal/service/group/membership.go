package group

// SetMembershipInvalidator wires the generation shared by policy input caches.
// Group filter/selection writes notify after persistence, including a committed
// deletion whose subsequent scope cleanup fails. Wire before serving requests.
func (s *Service) SetMembershipInvalidator(invalidate func()) { s.membershipInvalidator = invalidate }

func (s *Service) invalidateMembership() {
	if s.membershipInvalidator != nil {
		s.membershipInvalidator()
	}
}
