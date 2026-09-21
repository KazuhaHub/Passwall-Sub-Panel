// Package servermigration previews an offline, PSP-managed backend conversion.
// It performs no upstream calls and never serializes configuration or secrets.
package servermigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	repo    ports.ServerMigrationRepo
	catalog ports.CoreCatalog
}

func New(repo ports.ServerMigrationRepo, catalog ports.CoreCatalog) *Service {
	return &Service{repo: repo, catalog: catalog}
}

// validateCore uses the same published catalog the node runtime reads. An explicit
// restricted acknowledgment is a deliberate compatibility choice, not a claim that
// its clients are broadly compatible.
//
// THE TIER SET HERE IS NOT THE SAME AS "NOT RECOMMENDED". `config_verified` is
// absent deliberately: its name says the configuration was exercised, not that a
// client was observed connecting, and every install gate in the panel requires the
// handshake evidence as well. The Node project refuses a selectable release
// without config evidence; this refuses one without handshake evidence, and the two
// together are what "reviewed" means.
func validateCore(document ports.CoreCatalogDocument, version string, allowRestricted bool) (ports.CoreRelease, error) {
	release, err := document.Resolve(string(domain.NodeCoreXray), version)
	if err != nil {
		return ports.CoreRelease{}, fmt.Errorf("%w: core_not_verified", domain.ErrValidation)
	}
	if release.RequiresConfirmation && !allowRestricted {
		return release, fmt.Errorf("%w: core_ack_required", domain.ErrValidation)
	}
	if release.Tier != domain.CoreTierRecommended && release.Tier != domain.CoreTierVerified && release.Tier != domain.CoreTierRestricted {
		return release, fmt.Errorf("%w: core_not_verified", domain.ErrValidation)
	}
	if !release.Evidence.SourceAudited || !release.Evidence.ConfigTested || !release.Evidence.HandshakeTested {
		return release, fmt.Errorf("%w: core_not_verified", domain.ErrValidation)
	}
	return release, nil
}

func (s *Service) Preview(ctx context.Context, panelID int64, coreVersion string, allowRestrictedReality bool) (*domain.ServerMigrationPreview, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("migration repository unavailable")
	}
	if panelID <= 0 {
		return nil, fmt.Errorf("%w: invalid server identifier", domain.ErrValidation)
	}
	snapshot, err := s.repo.Load(ctx, panelID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Panel == nil {
		return nil, fmt.Errorf("%w: server", domain.ErrNotFound)
	}
	if snapshot.Panel.ID != panelID {
		return nil, fmt.Errorf("%w: server scope mismatch", domain.ErrValidation)
	}
	// ONE READ FOR THE WHOLE PREVIEW. Both the default and the validation come from
	// the same document, so a refresh landing between them cannot make the preview
	// report a recommended version that its own validation then refuses.
	document, err := s.catalog.Document(ctx)
	if err != nil {
		return nil, fmt.Errorf("core catalog unavailable: %w", err)
	}
	recommended, err := document.Recommended(string(domain.NodeCoreXray))
	if err != nil {
		return nil, fmt.Errorf("core catalog unavailable: %w", err)
	}
	explicit := strings.TrimSpace(coreVersion) != ""
	if !explicit {
		coreVersion = snapshot.Panel.XrayVersion
		if strings.TrimSpace(coreVersion) == "" {
			coreVersion = recommended.Version
		}
	}
	preview := &domain.ServerMigrationPreview{
		ServerID: panelID, ServerName: snapshot.Panel.Name, CoreVersion: coreVersion,
		RecommendedCoreVersion: recommended.Version, AllowRestrictedReality: allowRestrictedReality,
		NodeCount: len(snapshot.Nodes), ClientCount: len(snapshot.Clients),
		Blockers: snapshot.Blockers(), Warnings: append(snapshot.Warnings(), domain.MigrationIssue{Code: "managed_scope"}),
	}
	release, coreErr := validateCore(document, coreVersion, allowRestrictedReality)
	if release.Version != "" {
		preview.CoreVersion, preview.CoreRequiresAck = release.Version, release.RequiresConfirmation
		// Native desired config uses a canonical acknowledgement: it must be
		// true exactly for a restricted release, never an unrelated broad core.
		preview.AllowRestrictedReality = release.RequiresConfirmation && allowRestrictedReality
	}
	if coreErr != nil {
		code := "core_not_verified"
		if release.RequiresConfirmation && !allowRestrictedReality {
			code = "core_ack_required"
		}
		preview.Blockers = append(preview.Blockers, domain.MigrationIssue{Code: code})
	} else {
		if release.RequiresConfirmation {
			preview.Warnings = append(preview.Warnings, domain.MigrationIssue{Code: "restricted_core"})
		}
		if release.Reality.ServerMinClientVer != "" {
			for _, n := range snapshot.Nodes {
				if n != nil && isReality(n.StreamSettings) {
					preview.Warnings = append(preview.Warnings, domain.MigrationIssue{Code: "reality_compatibility_normalization", NodeID: n.ID})
				}
			}
		}
	}
	if explicit {
		previous, normalizeErr := domain.NormalizeCoreVersion(snapshot.Panel.XrayVersion)
		if normalizeErr != nil || previous != preview.CoreVersion {
			preview.Warnings = append(preview.Warnings, domain.MigrationIssue{Code: "core_version_changed"})
		}
	}
	preview.Fingerprint = snapshot.Fingerprint(preview.CoreVersion, preview.AllowRestrictedReality)
	preview.CanMigrate = len(preview.Blockers) == 0
	return preview, nil
}

func isReality(raw string) bool {
	// Only a boolean escapes this parser; neither malformed JSON nor secret
	// values may enter an error message or preview response.
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return false
	}
	security, _ := object["security"].(string)
	return strings.EqualFold(strings.TrimSpace(security), "reality")
}
