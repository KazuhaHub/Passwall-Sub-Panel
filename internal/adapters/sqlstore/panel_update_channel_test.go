package sqlstore

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestPanelUpdateChannelStorageRoundTripKeepsRawLegacyAndFutureValues(t *testing.T) {
	r := newPanelRepo(t)
	for i, channel := range []domain.PanelUpdateChannel{"", domain.PanelUpdateStable, domain.PanelUpdateBeta, "future-custom"} {
		p := &domain.Panel{Kind: domain.PanelKindPSP, Name: strings.Repeat("p", i+1), URL: "psp://agt_test", UpdateChannel: channel}
		if err := r.Save(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		got, err := r.GetByID(t.Context(), p.ID)
		if err != nil || got.UpdateChannel != channel {
			t.Fatalf("raw channel round trip failed: %v", err)
		}
		remark := "display only"
		if err := r.UpdateNativeMetadata(t.Context(), p.ID, nil, &remark, nil); err != nil {
			t.Fatal(err)
		}
		got, err = r.GetByID(t.Context(), p.ID)
		if err != nil || got.UpdateChannel != channel || got.Remark != remark {
			t.Fatal("omitted channel changed stored preference")
		}
	}
}

func TestNativeMetadataPreferenceDoesNotRewriteIdentitySecretsCoreStreamsOrProbes(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	raw := "pspn_" + strings.Repeat("a", 43)
	panel, agent := nativeCredentialTestIdentity(t, "agt_channel_narrow", raw)
	if err := repos.NativeAgentProvisioning.CreateWithCredential(t.Context(), panel, agent, raw); err != nil {
		t.Fatal(err)
	}
	r := repos.XUIPanel.(*xuiPanelRepo)
	when := time.Now().UTC().Truncate(time.Second)
	if err := r.UpdateVersion(t.Context(), panel.ID, "observed-node", "observed-core", &when); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateIPLimitEnforcement(t.Context(), panel.ID, domain.IPLimitEnforcementEnforced, when); err != nil {
		t.Fatal(err)
	}
	var beforePanel, afterPanel xuiPanelRow
	var beforeAgent, afterAgent nodeAgentRow
	if err := db.First(&beforePanel, panel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("panel_id = ?", panel.ID).First(&beforeAgent).Error; err != nil {
		t.Fatal(err)
	}
	beforeStreams, err := repos.NodeAgent.ListStreams(t.Context(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	channel := domain.PanelUpdateBeta
	if err := r.UpdateNativeMetadata(t.Context(), panel.ID, nil, nil, &channel); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&afterPanel, panel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("panel_id = ?", panel.ID).First(&afterAgent).Error; err != nil {
		t.Fatal(err)
	}
	afterStreams, err := repos.NodeAgent.ListStreams(t.Context(), agent.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if afterPanel.UpdateChannel != string(channel) {
		t.Fatal("preference not persisted")
	}
	// Only preference and normal row-update time are owned by this write.
	afterPanel.UpdateChannel, afterPanel.UpdatedAt = beforePanel.UpdateChannel, beforePanel.UpdatedAt
	if !reflect.DeepEqual(beforePanel, afterPanel) || !reflect.DeepEqual(beforeAgent, afterAgent) || !reflect.DeepEqual(beforeStreams, afterStreams) {
		t.Fatal("native metadata write changed non-metadata state")
	}
	gotRaw, err := repos.NativeAgentProvisioning.GetCredential(t.Context(), panel.ID)
	if err != nil || gotRaw != raw {
		t.Fatal("fixed encrypted credential changed")
	}
}

func TestNativeMetadataRequiresOriginalNativeRecord(t *testing.T) {
	r := newPanelRepo(t)
	p := seedPanel(t, r)
	channel := domain.PanelUpdateBeta
	if err := r.UpdateNativeMetadata(t.Context(), p.ID, nil, nil, &channel); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("third-party write: %v", err)
	}
	if err := r.UpdateNativeMetadata(t.Context(), p.ID+100, nil, nil, &channel); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing record: %v", err)
	}
	if err := r.UpdateNativeMetadata(t.Context(), 0, nil, nil, &channel); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid record: %v", err)
	}
}

func TestServerMigrationRetainsRawUpdateChannelAndLegacyStableDefault(t *testing.T) {
	for _, channel := range []domain.PanelUpdateChannel{"", domain.PanelUpdateBeta, "future-custom"} {
		t.Run(string(channel), func(t *testing.T) {
			f := newServerMigrationFixture(t)
			if err := f.db.Model(&xuiPanelRow{}).Where("id = ?", f.panelID).Update("update_channel", string(channel)).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.repos.ServerMigration.Apply(t.Context(), f.panelID, f.fingerprint(t), f.agent, f.raw); err != nil {
				t.Fatal(err)
			}
			got, err := f.repos.XUIPanel.GetByID(t.Context(), f.panelID)
			if err != nil || got.Kind != domain.PanelKindPSP || got.UpdateChannel != channel || got.UpdateChannel.Effective() != channel.Effective() {
				t.Fatal("migration rewrote the original preference or changed its effective default")
			}
		})
	}
}
