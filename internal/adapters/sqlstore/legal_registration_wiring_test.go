package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/registration"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"golang.org/x/crypto/bcrypt"
)

type legalRegistrationSettings struct{ value ports.UISettings }

func (s legalRegistrationSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return s.value, nil
}
func (s legalRegistrationSettings) Save(context.Context, ports.UISettings) error {
	return errors.New("read-only settings fixture")
}

type legalEmptySelector struct{}

func (legalEmptySelector) NodesFor(context.Context, *domain.Group) ([]*domain.Node, error) {
	return nil, nil
}

// The registration service deliberately sees a stale settings version. Only
// the production SQL writer can reject a publication that happened since load.
func TestLegalRegistration_ProductionWiringRejectsStaleVersion(t *testing.T) {
	for _, path := range []string{"verified", "immediate", "resume"} {
		t.Run(path, func(t *testing.T) {
			db, repos := legalTestRepos(t)
			ctx := context.Background()
			g := &domain.Group{Name: "legal-registration"}
			if err := repos.Group.Create(ctx, g); err != nil {
				t.Fatal(err)
			}
			enableLegal(t, repos)
			if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
				t.Fatal(err)
			}
			us := user.New(repos.User, repos.Group, nil, nil, legalEmptySelector{}, nil, nil, repos.ScopedSettings)
			reg := registration.New(registration.Deps{Users: us, Groups: repos.Group, Tokens: repos.AuthToken, Settings: legalRegistrationSettings{ports.UISettings{RegistrationEnabled: true, RegistrationDefaultGroupID: g.ID, RegistrationAllowUnverified: path == "immediate", LegalEnabled: true, LegalConsentVersion: 1}}})
			email := fmt.Sprintf("%s@example.com", path)
			var existing *domain.User
			if path == "resume" {
				u := consentUser(80)
				u.UPN, u.GroupID = email, g.ID
				if err := repos.User.Create(ctx, u); err != nil {
					t.Fatal(err)
				}
				existing = u
			}
			if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "major", true)); err != nil {
				t.Fatal(err)
			}
			_, err := reg.Register(ctx, registration.RegisterInput{Email: email, Password: "GoodPass123", AcceptedConsentVersion: 1})
			if !errors.Is(err, domain.ErrLegalConsentOutdated) {
				t.Fatalf("stale %s signup: %v", path, err)
			}
			if existing == nil {
				if _, err := repos.User.GetByUPN(ctx, email); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("stale signup created user: %v", err)
				}
			} else {
				stored, err := repos.User.GetByID(ctx, existing.ID)
				if err != nil || stored.PasswordHash != existing.PasswordHash || stored.TokenVersion != 0 {
					t.Fatalf("stale resume mutated credentials %+v: %v", stored, err)
				}
			}
			var count int64
			if err := db.Model(&legalConsentRow{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("stale consent records %d %v", count, err)
			}
		})
	}
}

func TestLegalRegistration_ProductionWiringCreatesConsent(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	g := &domain.Group{Name: "legal-registration"}
	if err := repos.Group.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
		t.Fatal(err)
	}
	us := user.New(repos.User, repos.Group, nil, nil, legalEmptySelector{}, nil, nil, repos.ScopedSettings)
	// Immediate signup has no email side effects or external nodes in this fixture.
	reg := registration.New(registration.Deps{Users: us, Groups: repos.Group, Settings: legalRegistrationSettings{ports.UISettings{RegistrationEnabled: true, RegistrationAllowUnverified: true, RegistrationDefaultGroupID: g.ID, LegalEnabled: true, LegalConsentVersion: 1}}})
	if _, err := reg.Register(ctx, registration.RegisterInput{Email: "consented@example.com", Password: "GoodPass123", AcceptedConsentVersion: 1}); err != nil {
		t.Fatal(err)
	}
	u, err := repos.User.GetByUPN(ctx, "consented@example.com")
	if err != nil || !u.Enabled || !u.SelfRegistered {
		t.Fatalf("created %+v: %v", u, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("GoodPass123")); err != nil {
		t.Fatal(err)
	}
	var record legalConsentRow
	if err := db.First(&record, "user_id = ?", u.ID).Error; err != nil || record.Method != "register" || record.ConsentVersion != 1 {
		t.Fatalf("record %+v: %v", record, err)
	}
}
