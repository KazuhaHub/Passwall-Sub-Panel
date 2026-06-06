// Package jwtutil is a thin wrapper over golang-jwt exposing two operations:
// issuance and verification of access/refresh tokens.
package jwtutil

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Claims is the JWT payload issued by the panel.
type Claims struct {
	UserID int64       `json:"uid"`
	UPN    string      `json:"upn"`
	Role   domain.Role `json:"r"`
	// TokenVersion mirrors User.TokenVersion at issue time. Auth
	// middleware compares this against the live row and 401s on
	// mismatch, so admin disable / role demote / password change
	// revoke every JWT signed before the bump. Omitted from JSON when
	// zero so legacy tokens (issued before this field existed) still
	// pass the default user.TokenVersion == 0 check after upgrade.
	TokenVersion int `json:"tv,omitempty"`
	// FirstFactor records which credential satisfied the FIRST authentication step
	// on a 2fa_pending token: "password" or "passkey". The 2FA challenge uses it to
	// decide whether a passkey may serve as the SECOND factor — a passwordless
	// passkey login must not be completable with the same passkey (one factor,
	// twice). Empty on access/refresh tokens.
	FirstFactor string `json:"ff,omitempty"`
	jwt.RegisteredClaims
}

const (
	SubjectAccess  = "access"
	SubjectRefresh = "refresh"
	// SubjectPending marks the short-lived token issued after password+captcha
	// succeed but before a 2FA code is verified. It is NOT an access token —
	// /auth/2fa/verify trades it (plus a valid code) for the real pair.
	SubjectPending = "2fa_pending"

	// First-factor values stamped onto a 2fa_pending token.
	FirstFactorPassword = "password"
	FirstFactorPasskey  = "passkey"
)

// pendingTTL bounds how long a user has to enter their 2FA code after the
// password step. Short, since the secret is already proven by password.
const pendingTTL = 5 * time.Minute

// Params is the live-tunable subset of JWT issuance — TTLs and the "iss"
// claim. Resolved fresh on every IssueAccess/IssueRefresh so admin edits
// take effect on the next login without a restart.
type Params struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Issuer     string
}

// ParamsCache is an atomic, lock-free holder for live JWT issuance params.
// The app updates it when Admin Settings are saved; token issuance reads it
// without hitting the DB during login bursts.
type ParamsCache struct {
	current atomic.Pointer[Params]
}

func NewParamsCache(initial Params) *ParamsCache {
	c := &ParamsCache{}
	c.Store(initial)
	return c
}

func (c *ParamsCache) Load() Params {
	if c == nil {
		return defaultParams()
	}
	p := c.current.Load()
	if p == nil {
		return defaultParams()
	}
	return *p
}

func (c *ParamsCache) Store(p Params) {
	if c == nil {
		return
	}
	if p.AccessTTL <= 0 {
		p.AccessTTL = 120 * time.Minute
	}
	if p.RefreshTTL <= 0 {
		p.RefreshTTL = 7 * 24 * time.Hour
	}
	if p.Issuer == "" {
		p.Issuer = "passwall-sub-panel"
	}
	c.current.Store(&p)
}

func defaultParams() Params {
	return Params{
		AccessTTL:  120 * time.Minute,
		RefreshTTL: 7 * 24 * time.Hour,
		Issuer:     "passwall-sub-panel",
	}
}

type Issuer struct {
	secret []byte
	params func() Params
}

// NewIssuer takes a closure rather than fixed values so that JWT TTLs and
// the issuer string can be edited from Admin → Settings and applied on the
// next token issue.
func NewIssuer(secret string, params func() Params) *Issuer {
	return &Issuer{secret: []byte(secret), params: params}
}

// AccessTTL / RefreshTTL expose the current TTL values so SSO callback
// handlers can match the access-cookie's Max-Age to the access token's
// natural expiry.
func (i *Issuer) AccessTTL() time.Duration  { return i.params().AccessTTL }
func (i *Issuer) RefreshTTL() time.Duration { return i.params().RefreshTTL }

// IssueAccess signs and returns an access token.
func (i *Issuer) IssueAccess(uid int64, upn string, role domain.Role, tokenVersion int) (string, error) {
	p := i.params()
	return i.issue(uid, upn, role, tokenVersion, "", SubjectAccess, p.AccessTTL, p.Issuer)
}

// IssueRefresh signs and returns a refresh token.
func (i *Issuer) IssueRefresh(uid int64, upn string, role domain.Role, tokenVersion int) (string, error) {
	p := i.params()
	return i.issue(uid, upn, role, tokenVersion, "", SubjectRefresh, p.RefreshTTL, p.Issuer)
}

// IssuePending signs a short-lived 2fa_pending token carrying the user id and the
// first factor already satisfied, used to resume login once a 2FA code/assertion
// is verified.
func (i *Issuer) IssuePending(uid int64, upn string, role domain.Role, tokenVersion int, firstFactor string) (string, error) {
	return i.issue(uid, upn, role, tokenVersion, firstFactor, SubjectPending, pendingTTL, i.params().Issuer)
}

// ParsePending verifies signature, time window and the 2fa_pending subject.
func (i *Issuer) ParsePending(tokenStr string) (*Claims, error) {
	return i.parse(tokenStr, SubjectPending)
}

func (i *Issuer) issue(uid int64, upn string, role domain.Role, tokenVersion int, firstFactor, sub string, ttl time.Duration, iss string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:       uid,
		UPN:          upn,
		Role:         role,
		TokenVersion: tokenVersion,
		FirstFactor:  firstFactor,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    iss,
			Subject:   sub,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(i.secret)
}

// Parse verifies signature and time window and returns the embedded Claims.
func (i *Issuer) Parse(tokenStr string) (*Claims, error) {
	return i.parse(tokenStr, "")
}

// ParseAccess verifies signature, time window and the access-token subject.
func (i *Issuer) ParseAccess(tokenStr string) (*Claims, error) {
	return i.parse(tokenStr, SubjectAccess)
}

// ParseRefresh verifies signature, time window and the refresh-token subject.
func (i *Issuer) ParseRefresh(tokenStr string) (*Claims, error) {
	return i.parse(tokenStr, SubjectRefresh)
}

func (i *Issuer) parse(tokenStr, expectedSubject string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return i.secret, nil
	},
		// Belt-and-suspenders alg pinning at the library level (the keyfunc
		// already rejects non-HMAC), require an exp claim, and bind the
		// token to our issuer. Changing the Issuer in Admin Settings
		// intentionally invalidates previously-issued tokens.
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(i.params().Issuer),
	)
	if err != nil {
		return nil, err
	}
	if c, ok := tok.Claims.(*Claims); ok && tok.Valid {
		if expectedSubject != "" && c.Subject != expectedSubject {
			return nil, errors.New("unexpected token subject")
		}
		return c, nil
	}
	return nil, errors.New("invalid token")
}
