package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type legalPublicationDuringSettingsSave struct {
	ports.SettingsRepo
	legal ports.LegalRepo
}

func (r legalPublicationDuringSettingsSave) Save(ctx context.Context, settings ports.UISettings) error {
	if err := r.SettingsRepo.Save(ctx, settings); err != nil {
		return err
	}
	_, err := r.legal.Publish(ctx, domain.LegalDraft{Kind: "privacy", Locale: "zh-CN", Content: "intervening major publication", ConsentBump: true, PublishedBy: 1})
	return err
}

func TestSettingsPut_ResponseUsesInterveningPublicationVersion(t *testing.T) {
	repos := legalHTTPRepos(t)
	ctx := t.Context()
	if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "terms", Locale: "en-US", Content: "first", PublishedBy: 1}); err != nil {
		t.Fatal(err)
	}
	wrapper := legalPublicationDuringSettingsSave{SettingsRepo: repos.Settings, legal: repos.Legal}
	w := requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(wrapper), http.MethodPut, `{"login_mode":"local_only","legal_enabled":true,"legal_consent_version":1}`)
	var response struct {
		Version int64 `json:"legal_consent_version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response.Version != 2 {
		t.Fatalf("response used pre-publication metadata: %d %+v", w.Code, response)
	}
	stored, err := repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil || stored.LegalConsentVersion != response.Version {
		t.Fatalf("response/storage mismatch %+v: %v", stored, err)
	}
}

func TestSettingsPut_PreservesLegalConsentVersion(t *testing.T) {
	stored := storedEverywhere()
	stored.LegalEnabled = true
	stored.LegalConsentVersion = 9
	repo := &policySettingsRepo{settings: stored}
	for _, payload := range []string{
		`{"login_mode":"local_only","site_title":"after","legal_consent_version":0}`,
		`{"login_mode":"local_only","legal_consent_version":999999}`,
		`{"login_mode":"local_only","legal_consent_version":{"forged":"metadata"}}`,
		`{"login_mode":"local_only"}`,
		`{"login_mode":"local_only","legal_enabled":null}`,
	} {
		w := requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, "/api/admin/settings/ui", payload)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT %d %s", w.Code, w.Body.String())
		}
		got, _, _ := repo.snapshot()
		if !got.LegalEnabled || got.LegalConsentVersion != 9 {
			t.Fatalf("old/forged request reset legal state: enabled=%t version=%d", got.LegalEnabled, got.LegalConsentVersion)
		}
		var response struct {
			Enabled bool  `json:"legal_enabled"`
			Version int64 `json:"legal_consent_version"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !response.Enabled || response.Version != 9 {
			t.Fatalf("response %+v", response)
		}
	}
	for _, tc := range []struct {
		payload string
		enabled bool
	}{{`{"login_mode":"local_only","legal_enabled":false}`, false}, {`{"login_mode":"local_only","legal_enabled":true}`, true}} {
		w := requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, "/api/admin/settings/ui", tc.payload)
		if w.Code != http.StatusOK {
			t.Fatalf("explicit toggle %d %s", w.Code, w.Body.String())
		}
		got, _, _ := repo.snapshot()
		if got.LegalEnabled != tc.enabled || got.LegalConsentVersion != 9 {
			t.Fatalf("toggle enabled=%t version=%d want %t/9", got.LegalEnabled, got.LegalConsentVersion, tc.enabled)
		}
	}
	got, _, _ := repo.snapshot()
	if !got.LegalEnabled || got.LegalConsentVersion != 9 {
		t.Fatalf("toggle changed publication metadata %+v", got)
	}
}

// A settings tab loaded before publication must not restore the old version
// even when the real SQL repository has already published another document.
func TestSettingsPut_RealPublicationVersionSurvivesStaleTab(t *testing.T) {
	repos := legalHTTPRepos(t)
	ctx := t.Context()
	if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "terms", Locale: "en-US", Content: "first", PublishedBy: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: "privacy", Locale: "zh-CN", Content: "major", ConsentBump: true, PublishedBy: 1}); err != nil {
		t.Fatal(err)
	}
	w := requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repos.Settings), http.MethodPut, `{"login_mode":"local_only","legal_enabled":true,"legal_consent_version":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("stale tab %d %s", w.Code, w.Body.String())
	}
	got, err := repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil || got.LegalConsentVersion != 2 || !got.LegalEnabled {
		t.Fatalf("publication state %+v: %v", got, err)
	}
}

func TestSettingsWritersKeepLegalPublicationAndDestinationOwnersSeparate(t *testing.T) {
	stored := storedEverywhere()
	stored.LegalEnabled, stored.LegalConsentVersion = true, 9
	stored.DestHitRetentionDays, stored.DestTrialRetentionDays, stored.DestUsageRetentionDays = 31, 12, 9
	stored.DestListRefreshHours, stored.DestPolicyApplyMinSeconds = 7, 45
	repo := &policySettingsRepo{settings: stored}
	w := requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, "/api/admin/settings/ui",
		`{"login_mode":"local_only","legal_enabled":false,"legal_consent_version":999,"dest_hit_retention_days":{"stale":true},"dest_trial_retention_days":0,"dest_usage_retention_days":1,"dest_list_refresh_hours":0,"dest_policy_apply_min_seconds":null}`)
	if w.Code != http.StatusOK {
		t.Fatalf("legal toggle: %d %s", w.Code, w.Body.String())
	}
	got, _, _ := repo.snapshot()
	if got.LegalEnabled || got.LegalConsentVersion != 9 || got.AccessControlSettings() != stored.AccessControlSettings() {
		t.Fatalf("legal toggle changed a different writer's state: legal=%t/%d destination=%+v", got.LegalEnabled, got.LegalConsentVersion, got.AccessControlSettings())
	}
	w = requestDestinationSettings(t, destinationRouter(repo), http.MethodPut, `{"dest_hit_retention_days":17}`)
	if w.Code != http.StatusOK {
		t.Fatalf("destination edit: %d %s", w.Code, w.Body.String())
	}
	got, _, _ = repo.snapshot()
	stored.DestHitRetentionDays = 17
	if got.LegalEnabled || got.LegalConsentVersion != 9 || got.AccessControlSettings() != stored.AccessControlSettings() {
		t.Fatalf("destination edit changed legal publication or unedited controls: legal=%t/%d destination=%+v", got.LegalEnabled, got.LegalConsentVersion, got.AccessControlSettings())
	}
}
