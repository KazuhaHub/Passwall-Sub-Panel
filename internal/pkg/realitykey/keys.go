// Package realitykey generates fresh X25519 keypairs and shortIDs for
// Reality inbound provisioning. Output formats match what 3X-UI / Xray
// expect: base64-url (no padding) for keys, lowercase hex for shortIds.
package realitykey

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"golang.org/x/crypto/curve25519"
)

// GenerateKeypair returns (privateKey, publicKey), both base64-url-no-pad
// encoded. The private key receives the standard X25519 clamping before
// scalar-base-mult.
func GenerateKeypair() (privateKey, publicKey string, err error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return "", "", err
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(priv[:]),
		base64.RawURLEncoding.EncodeToString(pub), nil
}

// GenerateShortID returns a random 8-char lowercase-hex shortId (4 bytes).
// Reality accepts shortIds of length 2-16 hex chars; 8 is a comfortable default.
func GenerateShortID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// PublicFromPrivate derives the public key of a base64-url (no padding)
// X25519 private key, in the same encoding GenerateKeypair uses.
func PublicFromPrivate(privateKey string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(privateKey)
	if err != nil || len(raw) != 32 {
		return "", errors.New("a REALITY private key is 32 bytes, base64-url encoded")
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(pub), nil
}
