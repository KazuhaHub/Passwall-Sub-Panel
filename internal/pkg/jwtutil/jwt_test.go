package jwtutil

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestTokenSubjectsAreEnforced(t *testing.T) {
	issuer := NewIssuer("test-secret", func() Params {
		return Params{
			AccessTTL:  time.Hour,
			RefreshTTL: time.Hour,
			Issuer:     "test",
		}
	})

	access, err := issuer.IssueAccess(1, "alice", domain.RoleUser, 0)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	refresh, err := issuer.IssueRefresh(1, "alice", domain.RoleUser, 0)
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}

	if _, err := issuer.ParseAccess(access); err != nil {
		t.Fatalf("ParseAccess(access): %v", err)
	}
	if _, err := issuer.ParseRefresh(refresh); err != nil {
		t.Fatalf("ParseRefresh(refresh): %v", err)
	}
	if _, err := issuer.ParseAccess(refresh); err == nil {
		t.Fatal("ParseAccess(refresh) succeeded, want error")
	}
	if _, err := issuer.ParseRefresh(access); err == nil {
		t.Fatal("ParseRefresh(access) succeeded, want error")
	}
}
