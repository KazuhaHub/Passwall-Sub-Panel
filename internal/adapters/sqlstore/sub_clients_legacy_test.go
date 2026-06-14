package sqlstore

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestMigrateLegacySubClients_NoLegacyData(t *testing.T) {
	if got := migrateLegacySubClients(nil, nil); got != nil {
		t.Fatalf("expected nil when there's no legacy data, got %v", got)
	}
}

func TestMigrateLegacySubClients_AttachByNameAndOrder(t *testing.T) {
	rules := []ports.SubClientRule{
		{Name: "Clash / mihomo", Keywords: []string{"clash", "mihomo"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "V2rayNG", Keywords: []string{"v2rayng"}, RenderFormat: "uri-list", Enabled: true},
		{Name: "V2RayN", Keywords: []string{"v2rayn", "v2ray"}, RenderFormat: "uri-list", Enabled: true},
	}
	imports := []ports.SubImportClient{
		{Name: "Clash Verge Rev", Platforms: []string{"windows"}, RenderFormat: "mihomo", ImportURLTemplate: "clash://x", Enabled: true, Sort: 10},
		{Name: "V2rayNG", Platforms: []string{"android"}, RenderFormat: "uri-list", ImportURLTemplate: "v2rayng://x", Enabled: true, Sort: 55},
		{Name: "V2rayN", Platforms: []string{"windows"}, RenderFormat: "uri-list", ImportURLTemplate: "x", Enabled: true, Sort: 50},
	}
	fams := migrateLegacySubClients(rules, imports)
	if len(fams) != 3 {
		t.Fatalf("want 3 families, got %d", len(fams))
	}
	// Clash Verge Rev → Clash family (name contains keyword "clash").
	if len(fams[0].Apps) != 1 || fams[0].Apps[0].Name != "Clash Verge Rev" {
		t.Fatalf("Clash Verge Rev should attach to Clash family: %+v", fams[0].Apps)
	}
	// V2rayNG → V2rayNG family, NOT V2RayN: the keyword-name match on the
	// earlier family wins, so the two apps stay independently toggleable.
	if len(fams[1].Apps) != 1 || fams[1].Apps[0].Name != "V2rayNG" {
		t.Fatalf("V2rayNG should attach to the V2rayNG family: %+v", fams[1].Apps)
	}
	// V2rayN → V2RayN family.
	if len(fams[2].Apps) != 1 || fams[2].Apps[0].Name != "V2rayN" {
		t.Fatalf("V2rayN should attach to the V2RayN family: %+v", fams[2].Apps)
	}
	// Apps shed their own render format (it's the family's now).
	if fams[0].Apps[0].ImportURLTemplate != "clash://x" {
		t.Fatalf("app fields should carry over: %+v", fams[0].Apps[0])
	}
}

func TestMigrateLegacySubClients_SynthesizesFamilyForOrphanFormat(t *testing.T) {
	// An import whose render format has no detection family must not be dropped
	// — a bare family is synthesized to hold it.
	rules := []ports.SubClientRule{
		{Name: "Clash", Keywords: []string{"clash"}, RenderFormat: "mihomo", Enabled: true},
	}
	imports := []ports.SubImportClient{
		{Name: "sing-box", Platforms: []string{"ios"}, RenderFormat: "sing-box", ImportURLTemplate: "sing-box://x", Enabled: true},
	}
	fams := migrateLegacySubClients(rules, imports)
	if len(fams) != 2 {
		t.Fatalf("want 2 families (1 rule + 1 synthesized), got %d", len(fams))
	}
	if fams[1].RenderFormat != "sing-box" || len(fams[1].Apps) != 1 || fams[1].Apps[0].Name != "sing-box" {
		t.Fatalf("synthesized family wrong: %+v", fams[1])
	}
}
