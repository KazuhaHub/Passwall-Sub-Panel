package sqlstore

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func TestDestinationUserAccessUsesOnlyDisplayColumnsAndPreservesMissingHistory(t *testing.T) {
	r := newDestDefinitionRepo(t)
	g := groupFromDomain(&domain.Group{Slug: "access-read", Name: "Access read"})
	if err := r.db.Create(g).Error; err != nil {
		t.Fatal(err)
	}
	owner := userFromDomain(&domain.User{UPN: "access-owner@example.test", UUID: "access-owner-uuid", SubToken: "access-owner-sub", GroupID: g.ID})
	creator := userFromDomain(&domain.User{UPN: "access-creator@example.test", UUID: "access-creator-uuid", SubToken: "access-creator-sub"})
	for _, user := range []*userRow{owner, creator} {
		if err := r.db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.UnixMilli(1791000000000).UTC()
	ex := domain.DestExemption{UserID: owner.ID, CreatedBy: creator.ID, Reason: "Display metadata"}
	if err := r.SaveExemption(t.Context(), &ex, true, now); err != nil {
		t.Fatal(err)
	}
	problem := errors.New("user access loaded private or large columns")
	if err := r.db.Callback().Query().Before("gorm:query").Register("user-access-display-guard", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "dest_lists":
			tx.AddError(problem)
		case "users":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "upn", "group_id"}) && !slices.Equal(tx.Statement.Selects, []string{"id", "upn"}) {
				tx.AddError(problem)
			}
		case "groups":
			if !slices.Equal(tx.Statement.Selects, []string{"id", "name"}) {
				tx.AddError(problem)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("user-access-display-guard") })
	access, err := r.UserAccess(t.Context(), owner.ID)
	if err != nil || access.UPN != owner.UPN || access.Group == nil || access.Group.ID != g.ID || access.Group.Mode != "open" || access.Exemption == nil || access.CreatedByUPN == nil || *access.CreatedByUPN != creator.UPN {
		t.Fatal("narrow account access metadata read failed")
	}
	if err := r.db.Delete(creator).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Delete(g).Error; err != nil {
		t.Fatal(err)
	}
	access, err = r.UserAccess(t.Context(), owner.ID)
	if err != nil || access.Group != nil || access.CreatedByUPN != nil || access.Exemption == nil || access.Exemption.CreatedBy != creator.ID {
		t.Fatal("deleted display identities were fabricated or hid the historical exemption")
	}
	if _, err := r.UserAccess(t.Context(), 9223372036854775807); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("missing access owner was not distinguished from no group or exemption")
	}
}

func TestDestinationUserAccessRejectsCorruptModeWithoutPartialResponse(t *testing.T) {
	r := newDestDefinitionRepo(t)
	g := groupFromDomain(&domain.Group{Slug: "access-corrupt", Name: "Access corrupt"})
	if err := r.db.Create(g).Error; err != nil {
		t.Fatal(err)
	}
	owner := userFromDomain(&domain.User{UPN: "access-corrupt@example.test", UUID: "access-corrupt-uuid", SubToken: "access-corrupt-sub", GroupID: g.ID})
	if err := r.db.Create(owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&destGroupModeRow{GroupID: g.ID, Mode: "invalid", Stage: "trial", ListIDs: jsonInt64s{}, UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	access, err := r.UserAccess(t.Context(), owner.ID)
	if !errors.Is(err, domain.ErrUnavailable) || access.UPN != "" || access.Group != nil || access.Exemption != nil {
		t.Fatal("corrupt group mode became a permissive or partial account response")
	}
}
