package user

// SetMembershipInvalidator lets policy input caches track committed membership
// changes. Nil preserves the uncached read path; wire before serving requests.
func (s *Service) SetMembershipInvalidator(invalidate func()) {
	s.membershipInvalidator = invalidate
}

func (s *Service) invalidateMembership() {
	if s.membershipInvalidator != nil {
		s.membershipInvalidator()
	}
}
