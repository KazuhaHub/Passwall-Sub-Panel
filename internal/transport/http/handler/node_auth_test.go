package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nodeCredentialLookupFunc func(context.Context, string) (*domain.NodeAgent, error)

func (f nodeCredentialLookupFunc) GetByCredentialSHA256(ctx context.Context, digest string) (*domain.NodeAgent, error) {
	return f(ctx, digest)
}

func TestNodeBearerAuthenticatorResolvesOpaqueCredentialDigest(t *testing.T) {
	credential := "pspn_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	wantDigest := sha256.Sum256([]byte(credential))
	auth, err := NewNodeBearerAuthenticator(nodeCredentialLookupFunc(func(_ context.Context, digest string) (*domain.NodeAgent, error) {
		if digest != hex.EncodeToString(wantDigest[:]) {
			t.Fatalf("digest = %q", digest)
		}
		return &domain.NodeAgent{AgentID: "agt_expected"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", nil)
	request.Header.Set("Authorization", "Bearer "+credential)
	got, err := auth.Authenticate(request)
	if err != nil || got != "agt_expected" {
		t.Fatalf("Authenticate = (%q, %v)", got, err)
	}
}

func TestNodeBearerAuthenticatorRejectsAmbiguousOrInvalidCredentials(t *testing.T) {
	lookups := 0
	auth, err := NewNodeBearerAuthenticator(nodeCredentialLookupFunc(func(context.Context, string) (*domain.NodeAgent, error) {
		lookups++
		return nil, errors.New("not found")
	}))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "wrong scheme", values: []string{"Basic abcdefghijklmnopqrstuvwxyz012345"}},
		{name: "too short", values: []string{"Bearer short"}},
		{name: "embedded whitespace", values: []string{"Bearer abcdefghijklmnopqrstuvwxyz 0123456789"}},
		{name: "duplicate headers", values: []string{"Bearer abcdefghijklmnopqrstuvwxyz0123456789", "Bearer zyxwvutsrqponmlkjihgfedcba9876543210"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", nil)
			for _, value := range test.values {
				request.Header.Add("Authorization", value)
			}
			if _, err := auth.Authenticate(request); !errors.Is(err, ErrNodeAuthentication) {
				t.Fatalf("Authenticate error = %v", err)
			}
		})
	}
	if lookups != 0 {
		t.Fatalf("invalid credentials reached repository %d times", lookups)
	}

	validUnknown := httptest.NewRequest(http.MethodPost, "/v1/node/sync", nil)
	validUnknown.Header.Set("Authorization", "Bearer abcdefghijklmnopqrstuvwxyz0123456789")
	if _, err := auth.Authenticate(validUnknown); !errors.Is(err, ErrNodeAuthentication) {
		t.Fatalf("unknown credential error = %v", err)
	}
	if lookups != 1 {
		t.Fatalf("valid credential lookup count = %d", lookups)
	}
}
