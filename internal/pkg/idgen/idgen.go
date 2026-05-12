// Package idgen centralizes ID / token generation to avoid duplicated logic.
package idgen

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/google/uuid"
)

// NewUUID returns a v4 UUID string (used for user.uuid / client.id).
func NewUUID() string {
	return uuid.NewString()
}

// NewSubToken returns a 32-byte URL-safe random token, base64-encoded without
// padding. Used for user.sub_token in subscription URLs.
func NewSubToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewPassword returns a 16-byte URL-safe random string (~22 chars) suitable
// as an initial password for a local account.
func NewPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
