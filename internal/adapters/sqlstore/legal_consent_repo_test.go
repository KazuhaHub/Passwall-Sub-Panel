package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func consentUser(n int) *domain.User {
	return &domain.User{UPN: fmt.Sprintf("legal%d@example.com", n), SubToken: fmt.Sprintf("legal-token-%d", n), UUID: "legal-test-uuid", Role: domain.RoleUser, GroupID: 1, Enabled: false, AutoDisabledReason: domain.DisabledPendingEmailVerify, SelfRegistered: true, PasswordHash: "old-hash", SSOProvider: domain.SSOProviderLocal}
}

func enableLegal(t *testing.T, repos ports.Repos) {
	t.Helper()
	if err := repos.Settings.Save(context.Background(), ports.UISettings{LegalEnabled: true}); err != nil {
		t.Fatal(err)
	}
}

func TestLegalConsent_PendingReplayAndMajorUpdate(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	u := consentUser(1)
	if err := repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
		t.Fatal(err)
	}
	s, err := repos.Legal.Status(ctx, u.ID)
	if err != nil || !s.Pending || s.ConsentVersion != 1 {
		t.Fatalf("initial status %+v: %v", s, err)
	}
	for _, stale := range []int64{0, -1, 2} {
		if err := repos.Legal.Accept(ctx, u.ID, stale); !errors.Is(err, domain.ErrLegalConsentOutdated) {
			t.Fatalf("version %d: %v", stale, err)
		}
	}
	if err := repos.Legal.Accept(ctx, u.ID, 1); err != nil {
		t.Fatal(err)
	}
	var first legalConsentRow
	if err := db.First(&first, "user_id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if first.Method != "prompt" || first.AcceptedAt.IsZero() {
		t.Fatalf("record %+v", first)
	}
	if err := repos.Legal.Accept(ctx, u.ID, 1); err != nil {
		t.Fatal(err)
	}
	var replay legalConsentRow
	if err := db.First(&replay, "user_id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !first.AcceptedAt.Equal(replay.AcceptedAt) {
		t.Fatal("replay rewrote acceptance timestamp")
	}
	if s, err = repos.Legal.Status(ctx, u.ID); err != nil || s.Pending {
		t.Fatalf("accepted %+v: %v", s, err)
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "major", true)); err != nil {
		t.Fatal(err)
	}
	if s, err = repos.Legal.Status(ctx, u.ID); err != nil || !s.Pending || s.ConsentVersion != 2 {
		t.Fatalf("major %+v: %v", s, err)
	}
	if err := repos.Legal.Accept(ctx, u.ID, 1); !errors.Is(err, domain.ErrLegalConsentOutdated) {
		t.Fatalf("old acceptance: %v", err)
	}
}

func TestLegalConsent_DisabledNoPublicationAndStaff(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	u := consentUser(2)
	if err := repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if err := repos.Settings.Save(ctx, ports.UISettings{LegalEnabled: enabled}); err != nil {
			t.Fatal(err)
		}
		if s, err := repos.Legal.Status(ctx, u.ID); err != nil || s.Pending {
			t.Fatalf("no doc %+v: %v", s, err)
		}
		if err := repos.Legal.Accept(ctx, u.ID, 0); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("accept absent: %v", err)
		}
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&userRow{}).Where("id = ?", u.ID).Update("role", string(domain.RoleAdmin)).Error; err != nil {
		t.Fatal(err)
	}
	if s, err := repos.Legal.Status(ctx, u.ID); err != nil || s.Pending {
		t.Fatalf("staff %+v: %v", s, err)
	}
	if err := repos.Legal.Accept(ctx, u.ID, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("staff accept: %v", err)
	}
	if _, err := repos.Legal.Status(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	var count int64
	if err := db.Model(&legalConsentRow{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected records %d %v", count, err)
	}
}

func TestRegisteredUser_AtomicCreateAndResume(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	w := repos.User.(ports.RegisteredUserWriter)
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
		t.Fatal(err)
	}
	u := consentUser(3)
	for _, stale := range []int64{0, 2} {
		if err := w.CreateRegistered(ctx, u, stale); !errors.Is(err, domain.ErrLegalConsentOutdated) {
			t.Fatalf("stale create: %v", err)
		}
		if u.ID != 0 {
			t.Fatal("failed create exposed an ID")
		}
	}
	var count int64
	if err := db.Model(&userRow{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("stale left user %d: %v", count, err)
	}
	if err := w.CreateRegistered(ctx, u, 1); err != nil {
		t.Fatal(err)
	}
	var record legalConsentRow
	if err := db.First(&record, "user_id = ?", u.ID).Error; err != nil || record.Method != "register" {
		t.Fatalf("registration record %+v: %v", record, err)
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "major", true)); err != nil {
		t.Fatal(err)
	}
	if err := w.ResumeRegistration(ctx, u.ID, "new-hash", 1); !errors.Is(err, domain.ErrLegalConsentOutdated) {
		t.Fatalf("stale resume: %v", err)
	}
	stored, err := repos.User.GetByID(ctx, u.ID)
	if err != nil || stored.PasswordHash != "old-hash" || stored.TokenVersion != 0 {
		t.Fatalf("stale changed credentials %+v: %v", stored, err)
	}
	if err := w.ResumeRegistration(ctx, u.ID, "new-hash", 2); err != nil {
		t.Fatal(err)
	}
	stored, err = repos.User.GetByID(ctx, u.ID)
	if err != nil || stored.PasswordHash != "new-hash" || stored.TokenVersion != 1 {
		t.Fatalf("fresh resume %+v: %v", stored, err)
	}
	if err := db.Model(&userRow{}).Where("id = ?", u.ID).Updates(map[string]any{"enabled": true, "auto_disabled_reason": ""}).Error; err != nil {
		t.Fatal(err)
	}
	if err := w.ResumeRegistration(ctx, u.ID, "hijack-hash", 2); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("activated account resumed: %v", err)
	}
}

func TestRegisteredUser_ConsentFailureRollsBack(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%t", resume), func(t *testing.T) {
			db, repos := legalTestRepos(t)
			ctx := context.Background()
			w := repos.User.(ports.RegisteredUserWriter)
			enableLegal(t, repos)
			if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
				t.Fatal(err)
			}
			u := consentUser(4)
			if resume {
				if err := repos.User.Create(ctx, u); err != nil {
					t.Fatal(err)
				}
			}
			injected := errors.New("consent storage unavailable")
			const callback = "test:consent-failure"
			if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "legal_consents" {
					tx.AddError(injected)
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer db.Callback().Create().Remove(callback)
			var err error
			if resume {
				err = w.ResumeRegistration(ctx, u.ID, "new-hash", 1)
			} else {
				err = w.CreateRegistered(ctx, u, 1)
			}
			if !errors.Is(err, injected) {
				t.Fatalf("failure: %v", err)
			}
			if resume {
				stored, e := repos.User.GetByID(ctx, u.ID)
				if e != nil || stored.PasswordHash != "old-hash" || stored.TokenVersion != 0 {
					t.Fatalf("rollback %+v: %v", stored, e)
				}
			} else {
				var count int64
				if e := db.Model(&userRow{}).Count(&count).Error; e != nil || count != 0 || u.ID != 0 {
					t.Fatalf("create rollback %d ID%d: %v", count, u.ID, e)
				}
			}
		})
	}
}

func TestLegalConsent_AffectedUsersAndOrphanCleanup(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	users := []*domain.User{consentUser(5), consentUser(6), consentUser(7)}
	users[2].Role = domain.RoleAdmin
	for _, u := range users {
		if err := repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := repos.Legal.AffectedUsers(ctx); err != nil || n != 2 {
		t.Fatalf("affected %d: %v", n, err)
	}
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
		t.Fatal(err)
	}
	for _, u := range users[:2] {
		if err := repos.Legal.Accept(ctx, u.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := repos.Legal.AffectedUsers(ctx); err != nil || n != 2 {
		t.Fatalf("major count must include accepted users %d: %v", n, err)
	}
	if err := db.Delete(&userRow{}, users[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := repos.Legal.PurgeOrphans(ctx); err != nil || n != 1 {
		t.Fatalf("purged %d: %v", n, err)
	}
	if n, err := repos.Legal.PurgeOrphans(ctx); err != nil || n != 0 {
		t.Fatalf("repeat purge %d: %v", n, err)
	}
	var count int64
	if err := db.Model(&legalConsentRow{}).Where("user_id = ?", users[1].ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("live consent lost %d: %v", count, err)
	}
}

func TestRegisteredUser_DisabledAndUnpublishedRemainCompatible(t *testing.T) {
	for _, mode := range []string{"default-off", "enabled-no-doc", "disabled-published"} {
		t.Run(mode, func(t *testing.T) {
			db, repos := legalTestRepos(t)
			ctx := context.Background()
			if mode == "enabled-no-doc" {
				enableLegal(t, repos)
			}
			if mode == "disabled-published" {
				if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
					t.Fatal(err)
				}
			}
			u := consentUser(50)
			w := repos.User.(ports.RegisteredUserWriter)
			if err := w.CreateRegistered(ctx, u, 0); err != nil {
				t.Fatal(err)
			}
			if err := w.ResumeRegistration(ctx, u.ID, "new-hash", 0); err != nil {
				t.Fatal(err)
			}
			var n int64
			if err := db.Model(&legalConsentRow{}).Count(&n).Error; err != nil || n != 0 {
				t.Fatalf("legacy records %d: %v", n, err)
			}
		})
	}
}

func TestRegisteredUser_ConcurrentMajorPublication(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	enableLegal(t, repos)
	if _, err := repos.Legal.Publish(ctx, legalDraft("terms", "en-US", "first", false)); err != nil {
		t.Fatal(err)
	}
	const n = 16
	results := make(chan error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			results <- repos.User.(ports.RegisteredUserWriter).CreateRegistered(ctx, consentUser(100+i), 1)
		})
	}
	publication := make(chan domain.LegalPublication, 1)
	publishErr := make(chan error, 1)
	wg.Go(func() {
		<-start
		p, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "major", true))
		publication <- p
		publishErr <- err
	})
	close(start)
	wg.Wait()
	close(results)
	if err := <-publishErr; err != nil {
		t.Fatal(err)
	}
	p := <-publication
	if p.ConsentVersion != 2 {
		t.Fatalf("major version %d", p.ConsentVersion)
	}
	success := int64(0)
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, domain.ErrLegalConsentOutdated) {
			t.Fatal(err)
		}
	}
	var accounts int64
	if err := db.Model(&userRow{}).Count(&accounts).Error; err != nil || accounts != success {
		t.Fatalf("accounts %d successes %d: %v", accounts, success, err)
	}
	var records []legalConsentRow
	if err := db.Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if int64(len(records)) != success {
		t.Fatalf("consents %d successes %d", len(records), success)
	}
	for _, r := range records {
		if r.ConsentVersion != 1 || r.AcceptedAt.After(p.Document.PublishedAt) {
			t.Fatalf("obsolete consent committed after major publication: %+v vs %v", r, p.Document.PublishedAt)
		}
	}
}
