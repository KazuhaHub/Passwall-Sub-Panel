// Package auth issues JWTs and verifies them. Local-account login goes
// through user.Service.VerifyLocalPassword; SAML SSO lives in the saml
// subpackage. This top-level Service is the small surface the HTTP layer
// uses to mint and verify tokens.
package auth

import (
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
)

type Service struct {
	issuer *jwtutil.Issuer
}

func New(issuer *jwtutil.Issuer) *Service {
	return &Service{issuer: issuer}
}

// IssueTokens returns a freshly signed (access, refresh) pair.
func (s *Service) IssueTokens(u *domain.User) (access, refresh string, err error) {
	access, err = s.issuer.IssueAccess(u.ID, u.UPN, u.Role)
	if err != nil {
		return "", "", err
	}
	refresh, err = s.issuer.IssueRefresh(u.ID, u.UPN, u.Role)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// Verify parses and validates an access token.
func (s *Service) Verify(tokenStr string) (*jwtutil.Claims, error) {
	return s.issuer.Parse(tokenStr)
}

// AccessTTL / RefreshTTL expose the issuer's live TTL values for SSO
// callback handlers that need to match the cookie Max-Age to the token
// expiry. Read fresh on every call.
func (s *Service) AccessTTL() time.Duration  { return s.issuer.AccessTTL() }
func (s *Service) RefreshTTL() time.Duration { return s.issuer.RefreshTTL() }
