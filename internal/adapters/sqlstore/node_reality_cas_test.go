package sqlstore

import (
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCompareAndSwapRealityStreamPreservesConcurrentAdminEdit(t *testing.T) {
	ConfigureSecretKey("test-only-reality-cas-key")
	t.Cleanup(func() { ConfigureSecretKey("") })
	repo, ctx := newNodeTestRepo(t)
	oldStream := `{"security":"reality","realitySettings":{"settings":{"fingerprint":"firefox"}}}`
	chromeStream := `{"security":"reality","realitySettings":{"settings":{"fingerprint":"chrome"}}}`
	captured := time.Now().Add(-time.Hour)
	n := &domain.Node{
		PanelID: 9, InboundID: 1, DesiredProtocol: "vless", DesiredPort: 443,
		InboundSettings: `{"decryption":"none"}`, StreamSettings: oldStream,
		ConfigSyncedAt: &captured, ConfigSyncState: domain.ConfigSyncSynced,
	}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	// The migration has already read oldStream. An admin now changes the same
	// inbound, including fields that the migration must never restore.
	admin, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin.DesiredPort = 8443
	admin.InboundRemark = "admin edit"
	admin.StreamSettings = strings.Replace(oldStream, `"firefox"`, `"safari"`, 1)
	if err := repo.UpdateInboundConfig(ctx, admin); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.CompareAndSwapRealityStream(ctx, n.PanelID, n.ID, oldStream, chromeStream)
	if err != nil || changed {
		t.Fatalf("stale migration changed=%v err=%v", changed, err)
	}
	current, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.DesiredPort != 8443 || current.InboundRemark != "admin edit" || !strings.Contains(current.StreamSettings, `"safari"`) {
		t.Fatalf("admin edit was overwritten: port=%d remark=%q stream=%s", current.DesiredPort, current.InboundRemark, current.StreamSettings)
	}
	changed, err = repo.CompareAndSwapRealityStream(ctx, n.PanelID, n.ID, current.StreamSettings, chromeStream)
	if err != nil || !changed {
		t.Fatalf("fresh migration changed=%v err=%v", changed, err)
	}
	current, err = repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.DesiredPort != 8443 || current.InboundRemark != "admin edit" || current.StreamSettings != chromeStream || current.ConfigSyncState != domain.ConfigSyncPending {
		t.Fatalf("fresh migration altered unrelated intent: port=%d remark=%q stream=%s state=%s", current.DesiredPort, current.InboundRemark, current.StreamSettings, current.ConfigSyncState)
	}
	var row nodeRow
	if err := repo.db.First(&row, n.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(row.StreamSettings, secretPrefix) || strings.Contains(row.StreamSettings, `"fingerprint"`) {
		t.Fatal("normalized stream was not encrypted at rest")
	}
}
