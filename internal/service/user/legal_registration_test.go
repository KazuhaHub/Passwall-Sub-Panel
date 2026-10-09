package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestRegistration_MissingAtomicWriterFailsClosed(t *testing.T) {
	r := &memoryUserRepo{}
	s := New(r, &bfGroupRepo{g: &domain.Group{ID: 1}}, nil, nil, nil, nil, nil, nil)
	_, err := s.CreateLocal(context.Background(), CreateLocalInput{UPN: "registered@example.com", InitialPassword: "GoodPass123", GroupID: 1, SelfRegistered: true})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("missing atomic create writer: %v", err)
	}
	if len(r.byID) != 0 {
		t.Fatal("fallback created an account without atomic consent")
	}
	if err := s.ResumeRegistration(context.Background(), 1, "GoodPass123", 1); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("missing resume writer: %v", err)
	}
	if r.updateCalls != 0 {
		t.Fatal("fallback changed credentials without atomic consent")
	}
	// Ordinary administrator-created accounts retain the original writer path.
	if _, err := s.CreateLocal(context.Background(), CreateLocalInput{UPN: "admin-created@example.com", InitialPassword: "GoodPass123", GroupID: 1}); err != nil {
		t.Fatal(err)
	}
}
