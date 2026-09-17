package version

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
)

type nodeCompatibilityMatrix struct {
	SchemaVersion int `json:"schema_version"`
	PanelMajor    int `json:"panel_major"`
	Wire          struct {
		Endpoint           string `json:"endpoint"`
		MinProtocolVersion int    `json:"min_protocol_version"`
		MaxProtocolVersion int    `json:"max_protocol_version"`
		LegacyZeroMapsTo   int    `json:"legacy_zero_maps_to"`
	} `json:"wire"`
	Features struct {
		AgentUpgradeV1 struct {
			RequiredCapabilities []string `json:"required_capabilities"`
		} `json:"agent_upgrade_v1"`
	} `json:"features"`
	ReleasedNodes []struct {
		Version         string   `json:"version"`
		ProtocolVersion int      `json:"protocol_version"`
		BaseSync        string   `json:"base_sync"`
		RemoteUpgrade   string   `json:"remote_upgrade"`
		UpgradeMethods  []string `json:"upgrade_methods"`
	} `json:"released_nodes"`
}

func TestNodeV4CompatibilityMatrixMatchesSharedProtocolPolicy(t *testing.T) {
	raw, err := os.ReadFile("../../docs/compat/node-v4.json")
	if err != nil {
		t.Fatal(err)
	}
	var matrix nodeCompatibilityMatrix
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	if matrix.SchemaVersion != 1 || matrix.PanelMajor != 4 || matrix.Wire.Endpoint != "/v1/node/sync" ||
		matrix.Wire.MinProtocolVersion != nodeprotocol.MinSupportedProtocolVersion ||
		matrix.Wire.MaxProtocolVersion != nodeprotocol.MaxSupportedProtocolVersion ||
		matrix.Wire.LegacyZeroMapsTo != nodeprotocol.EffectiveProtocolVersion(0) {
		t.Fatalf("wire matrix drifted from shared protocol policy: %+v", matrix.Wire)
	}
	if !reflect.DeepEqual(matrix.Features.AgentUpgradeV1.RequiredCapabilities, nodeprotocol.AgentUpgradeCapabilities()) {
		t.Fatalf("upgrade capability matrix = %v, shared policy = %v",
			matrix.Features.AgentUpgradeV1.RequiredCapabilities, nodeprotocol.AgentUpgradeCapabilities())
	}
	wantReleases := []string{"v0.0.1-beta1", "v0.0.1-beta2", "v0.0.1-beta3", "v0.0.1-beta4", "v0.0.1-beta5", "v0.0.1-beta6", "v0.0.1-beta7", "v0.0.1-beta8"}
	if len(matrix.ReleasedNodes) != len(wantReleases) {
		t.Fatalf("released node matrix has %d rows, want %d", len(matrix.ReleasedNodes), len(wantReleases))
	}
	for i, release := range matrix.ReleasedNodes {
		if release.Version != wantReleases[i] || release.ProtocolVersion != nodeprotocol.ProtocolVersion1 || release.BaseSync != "supported" {
			t.Fatalf("released node row %d = %+v", i, release)
		}
		if i < 2 && (release.RemoteUpgrade != "unsupported" || len(release.UpgradeMethods) != 0) {
			t.Fatalf("legacy release unexpectedly promises remote upgrade: %+v", release)
		}
		wantMethods := []string{"linux-systemd"}
		if release.Version == "v0.0.1-beta5" || release.Version == "v0.0.1-beta6" || release.Version == "v0.0.1-beta7" || release.Version == "v0.0.1-beta8" {
			wantMethods = append(wantMethods, "managed-docker")
		}
		if i >= 2 && (release.RemoteUpgrade != "conditional" || !reflect.DeepEqual(release.UpgradeMethods, wantMethods)) {
			t.Fatalf("upgrade-capable release matrix drifted: %+v", release)
		}
	}
}
