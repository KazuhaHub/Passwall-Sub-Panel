package sqlstore

import (
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestDestinationSettingsPersistKVAndRoundTrip(t *testing.T) {
	db, _, _, _, _ := policyMintFixture(t)
	values := map[string]string{"hit_retention_days": "40", "trial_retention_days": "30", "usage_retention_days": "3", "list_refresh_hours": "6", "policy_apply_min_seconds": "120"}
	for key, value := range values {
		if err := db.Create(&settingRow{Type: "dest", Name: key, Value: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := newKVSettingsRepo(db)
	s, err := repo.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(s)
	var decoded map[string]json.RawMessage
	_ = json.Unmarshal(body, &decoded)
	for key, value := range values {
		if string(decoded["dest_"+key]) != value {
			t.Fatalf("persisted destination %s disappeared: %s", key, decoded["dest_"+key])
		}
	}
	s.SiteTitle = "other settings edit"
	if err := repo.Save(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	var rows []settingRow
	if err := db.Where("type = ?", "dest").Find(&rows).Error; err != nil || len(rows) != 5 {
		t.Fatalf("dest settings rows=%d / %v", len(rows), err)
	}
	for _, row := range rows {
		if row.Value != values[row.Name] {
			t.Fatalf("round trip reset %s: %s", row.Name, row.Value)
		}
	}
}

func TestDestinationSettingsKeepRawZeroAndRejectDecodeErrors(t *testing.T) {
	db, _, _, _, _ := policyMintFixture(t)
	repo := newKVSettingsRepo(db)
	loaded, err := repo.Load(t.Context(), ports.UISettings{})
	if err != nil || loaded.DestinationSettings() != (domain.DestinationSettings{}) || loaded.DestinationSettings().Effective() != domain.DefaultDestinationSettings() {
		t.Fatalf("missing raw keys/defaults drifted: %+v / %v", loaded.DestinationSettings(), err)
	}
	if err := repo.Save(t.Context(), ports.UISettings{}); err != nil {
		t.Fatal(err)
	}
	var rows []settingRow
	if err := db.Where("type = ?", "dest").Find(&rows).Error; err != nil || len(rows) != 5 {
		t.Fatalf("first-save destination defaults: %d / %v", len(rows), err)
	}
	for _, row := range rows {
		if row.Value != "0" {
			t.Fatalf("missing preference became explicit default: %+v", row)
		}
	}
	for _, sample := range []struct {
		raw  string
		want int
	}{{"0", 7}, {"-1", 7}, {"361", 30}} {
		value := sample.raw
		if err := db.Model(&settingRow{}).Where("type = ? AND name = ?", "dest", "trial_retention_days").UpdateColumn("value", value).Error; err != nil {
			t.Fatal(err)
		}
		loaded, err := repo.Load(t.Context(), ports.UISettings{})
		if err != nil || loaded.DestinationSettings().EffectiveTrialRetentionDays() != sample.want {
			t.Fatalf("stored trial setting %s was not bounded: %+v / %v", value, loaded.DestinationSettings(), err)
		}
	}
	if err := db.Model(&settingRow{}).Where("type = ? AND name = ?", "dest", "trial_retention_days").UpdateColumn("value", "broken").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Load(t.Context(), ports.UISettings{}); err == nil {
		t.Fatal("malformed integer was silently defaulted")
	}
}
