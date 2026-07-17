package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncryptDecryptStringRoundTrip(t *testing.T) {
	for _, keyLen := range []int{16, 24, 32} {
		t.Run(strings.Repeat("x", keyLen), func(t *testing.T) {
			key := []byte(strings.Repeat("k", keyLen))
			for _, plaintext := range []string{"", "secret", "Unicode: 台灣 🔐"} {
				encoded, err := EncryptString(key, plaintext)
				if err != nil {
					t.Fatalf("EncryptString: %v", err)
				}
				if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
					t.Fatalf("ciphertext is not base64: %v", err)
				}
				got, err := DecryptString(key, encoded)
				if err != nil {
					t.Fatalf("DecryptString: %v", err)
				}
				if got != plaintext {
					t.Fatalf("plaintext = %q, want %q", got, plaintext)
				}
			}
		})
	}
}

func TestEncryptStringUsesFreshNonce(t *testing.T) {
	key := []byte("0123456789abcdef")
	one, err := EncryptString(key, "same")
	if err != nil {
		t.Fatal(err)
	}
	two, err := EncryptString(key, "same")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("two encryptions unexpectedly reused the same nonce")
	}
}

func TestAESGCMRejectsInvalidInputs(t *testing.T) {
	key := []byte("0123456789abcdef")
	if _, err := EncryptString([]byte("short"), "secret"); err == nil {
		t.Fatal("EncryptString accepted an invalid key length")
	}
	if _, err := DecryptString(key, "not base64!"); err == nil {
		t.Fatal("DecryptString accepted malformed base64")
	}
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := DecryptString(key, short); err == nil {
		t.Fatal("DecryptString accepted a payload without a full nonce")
	}

	encoded, err := EncryptString(key, "secret")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	if _, err := DecryptString(key, base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("DecryptString accepted a tampered authentication tag")
	}
	if _, err := DecryptString([]byte("fedcba9876543210"), encoded); err == nil {
		t.Fatal("DecryptString accepted the wrong key")
	}
	if _, err := DecryptString([]byte("short"), encoded); err == nil {
		t.Fatal("DecryptString accepted an invalid key length")
	}
}
