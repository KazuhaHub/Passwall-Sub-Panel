// Package jwtutil is a thin wrapper over golang-jwt exposing two operations:
// issuance and verification of access/refresh tokens.
package jwtutil

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Claims is the JWT payload issued by the panel.
type Claims struct {
	UserID   int64       `json:"uid"`
	Username string      `json:"u"`
	Role     domain.Role `json:"r"`
	Source   domain.UserSource `json:"src"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret        []byte
	accessTTL     time.Duration
	refreshTTL    time.Duration
	issuer        string
}

func NewIssuer(secret string, accessTTL, refreshTTL time.Duration, iss string) *Issuer {
	return &Issuer{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		issuer:     iss,
	}
}

// IssueAccess signs and returns an access token.
func (i *Issuer) IssueAccess(uid int64, username string, role domain.Role, src domain.UserSource) (string, error) {
	return i.issue(uid, username, role, src, "access", i.accessTTL)
}

// IssueRefresh signs and returns a refresh token.
func (i *Issuer) IssueRefresh(uid int64, username string, role domain.Role, src domain.UserSource) (string, error) {
	return i.issue(uid, username, role, src, "refresh", i.refreshTTL)
}

func (i *Issuer) issue(uid int64, username string, role domain.Role, src domain.UserSource, sub string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   uid,
		Username: username,
		Role:     role,
		Source:   src,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.issuer,
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
