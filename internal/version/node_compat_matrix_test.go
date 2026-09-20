package version

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
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
	// MinSupported names the OLDEST node version the panel still supports. It is
	// matched by POSITION in ReleasedNodes, never by comparing the strings: the
	// prerelease suffix sorts lexically, so v0.0.1-beta9 ranks ABOVE
	// v0.0.1-beta11 and a string comparison would invert the floor silently.
	MinSupported    string `json:"min_supported"`
	MinSupportedDoc string `json:"min_supported_doc"`
	ReleasedNodes   []struct {
		Version         string   `json:"version"`
		ProtocolVersion int      `json:"protocol_version"`
		BaseSync        string   `json:"base_sync"`
		RemoteUpgrade   string   `json:"remote_upgrade"`
		UpgradeMethods  []string `json:"upgrade_methods"`
	} `json:"released_nodes"`
}

// supportedFromFloor is the ONE derivation of "which nodes this panel supports",
// and it is deliberately a plain position slice with no version arithmetic.
// The CI matrix reads the same rule from the same file; the two agree because
// they agree on the data, not because they share code.
func supportedFromFloor(matrix nodeCompatibilityMatrix) []string {
	for i, release := range matrix.ReleasedNodes {
		if release.Version == matrix.MinSupported {
			versions := make([]string, 0, len(matrix.ReleasedNodes)-i)
			for _, row := range matrix.ReleasedNodes[i:] {
				versions = append(versions, row.Version)
			}
			return versions
		}
	}
	return nil
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

	// THIS FILE NO LONGER CARRIES A COPY OF EVERY RELEASED VERSION. It used to,
	// which meant each release had to be added in four places — here, the
	// manifest, and two workflows — and the copy could only ever confirm that
	// someone had remembered to update all four. What is asserted now is that the
	// manifest is SELF-CONSISTENT and that its floor resolves, so the supported
	// set is DERIVED from the data rather than restated beside it.
	if matrix.MinSupported == "" || matrix.MinSupportedDoc == "" {
		t.Fatal("the node matrix must declare min_supported and explain it")
	}
	supported := supportedFromFloor(matrix)
	if len(supported) == 0 {
		t.Fatalf("min_supported %q names no row in released_nodes, so the panel would support nothing",
			matrix.MinSupported)
	}
	if len(supported) == 0 || supported[0] != matrix.MinSupported {
		t.Fatalf("the derived support list must start at the floor, got %v", supported)
	}

	seen := make(map[string]struct{}, len(matrix.ReleasedNodes))
	for i, release := range matrix.ReleasedNodes {
		if release.Version == "" || release.ProtocolVersion != nodeprotocol.ProtocolVersion1 || release.BaseSync != "supported" {
			t.Fatalf("released node row %d = %+v", i, release)
		}
		if _, duplicate := seen[release.Version]; duplicate {
			t.Fatalf("released node row %d repeats %s", i, release.Version)
		}
		seen[release.Version] = struct{}{}
		// THE SHAPE OF A ROW, WITHOUT NAMING A VERSION. A release that promises no
		// remote upgrade carries no methods; one that promises a conditional
		// upgrade must carry the method that was actually verified. Encoding this
		// by version name is what made the old copy churn every release.
		switch release.RemoteUpgrade {
		case "unsupported":
			if len(release.UpgradeMethods) != 0 {
				t.Fatalf("row %s promises no remote upgrade but lists methods: %+v", release.Version, release.UpgradeMethods)
			}
		case "conditional":
			if !slices.Contains(release.UpgradeMethods, "linux-systemd") {
				t.Fatalf("row %s offers a conditional upgrade without the verified method: %+v", release.Version, release.UpgradeMethods)
			}
		default:
			t.Fatalf("row %s has an unknown remote_upgrade %q", release.Version, release.RemoteUpgrade)
		}
	}
}
