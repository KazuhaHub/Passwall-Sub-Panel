package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub)
}

func TestParseTrustKeysAcceptsTheDocumentedShape(t *testing.T) {
	first, second := testKey(t), testKey(t)
	keys, err := ParseTrustKeys(" primary=" + first + " , backup=" + second + " ")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys["primary"] != first || keys["backup"] != second {
		t.Fatalf("parsed %v", keys)
	}
}

func TestParseTrustKeysRefusesWhatWouldBeAmbiguous(t *testing.T) {
	key := testKey(t)
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"no separator", "primary" + key, "is not key_id=base64"},
		{"empty id", "=" + key, "is not key_id=base64"},
		{"empty key", "primary=", "is not key_id=base64"},
		{"nothing at all", " , ", "no keys found"},
		{"empty string", "", "no keys found"},
		{
			// Two keys under one id would make which one verifies depend on parse
			// order, and a rotation would silently pick a winner.
			name:  "duplicate id",
			value: "primary=" + key + ",primary=" + key,
			want:  "appears twice",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTrustKeys(tc.value)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A malformed key is refused at BOOT rather than degrading to "no keys".
// Trusting nothing because a key was mistyped looks identical to a working
// configuration from the operator's side, and they would only find out when a
// policy was rejected with no explanation.
func TestValidateRefusesAMalformedTrustKey(t *testing.T) {
	good := testKey(t)
	for _, tc := range []struct {
		name  string
		keys  map[string]string
		wants string
	}{
		{"base64 that does not decode", map[string]string{"k": "not base64!!"}, "is not base64"},
		{
			"a key that is not 32 bytes",
			map[string]string{"k": base64.StdEncoding.EncodeToString([]byte("too short"))},
			"want 32 for an ed25519 public key",
		},
		{"an entry with no id", map[string]string{"": good}, "empty key id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{JWTSecret: "x", PolicyTrustKeys: tc.keys}
			err := c.validate()
			if err == nil || !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wants)
			}
		})
	}

	c := &Config{JWTSecret: "x", PolicyTrustKeys: map[string]string{"k": good}}
	if err := c.validate(); err != nil {
		t.Fatalf("a well-formed key must validate: %v", err)
	}
	// No keys at all is the default and is not an error: it is the state of a
	// deployment that has not been given any.
	if err := (&Config{JWTSecret: "x"}).validate(); err != nil {
		t.Fatalf("an absent trust root must not fail validation: %v", err)
	}
}
