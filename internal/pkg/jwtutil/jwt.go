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
	jwt.RegisteredClaims
}

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
func (i *Issuer) IssueAccess(uid int64, upn string, role domain.Role) (string, error) {
	p := i.params()
	return i.issue(uid, upn, role, "access", p.AccessTTL, p.Issuer)
}

// IssueRefresh signs and returns a refresh token.
func (i *Issuer) IssueRefresh(uid int64, upn string, role domain.Role) (string, error) {
	p := i.params()
	return i.issue(uid, upn, role, "refresh", p.RefreshTTL, p.Issuer)
}

func (i *Issuer) issue(uid int64, upn string, role domain.Role, sub string, ttl time.Duration, iss string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: uid,
		UPN:    upn,
		Role:   role,
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
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return i.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if c, ok := tok.Claims.(*Claims); ok && tok.Valid {
		return c, nil
	}
	return nil, errors.New("invalid token")
}
