package realitykey

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/curve25519"
)

func TestGenerateKeypair(t *testing.T) {
	privateKey, publicKey, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(privateKey)
	if err != nil {
		t.Fatalf("private key encoding: %v", err)
	}
	publicBytes, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil {
		t.Fatalf("public key encoding: %v", err)
	}
	if len(privateBytes) != 32 || len(publicBytes) != 32 {
		t.Fatalf("key lengths = (%d, %d), want (32, 32)", len(privateBytes), len(publicBytes))
	}
	if privateBytes[0]&7 != 0 || privateBytes[31]&0x80 != 0 || privateBytes[31]&0x40 == 0 {
		t.Fatal("private key does not have the X25519 clamp bits set")
	}
	wantPublic, err := curve25519.X25519(privateBytes, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	if string(publicBytes) != string(wantPublic) {
		t.Fatal("public key does not correspond to the generated private key")
	}
}

func TestGenerateShortID(t *testing.T) {
	one, err := GenerateShortID()
	if err != nil {
		t.Fatal(err)
	}
	two, err := GenerateShortID()
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 8 {
		t.Fatalf("short ID length = %d, want 8", len(one))
	}
	if _, err := hex.DecodeString(one); err != nil {
		t.Fatalf("short ID is not lowercase hex: %v", err)
	}
	if one == two {
		t.Fatal("two generated short IDs unexpectedly matched")
	}
}
