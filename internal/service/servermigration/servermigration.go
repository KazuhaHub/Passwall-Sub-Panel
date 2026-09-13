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

	"github.com/KazuhaHub/passwall-node/corecatalog"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct{ repo ports.ServerMigrationRepo }

func New(repo ports.ServerMigrationRepo) *Service { return &Service{repo: repo} }

// ValidateCore uses the same published catalog as the PN runtime. An explicit
// restricted acknowledgment is a deliberate compatibility choice, not a claim
// that its clients are broadly compatible.
func ValidateCore(version string, allowRestricted bool) (corecatalog.Release, error) {
	release, err := corecatalog.Resolve(string(domain.NodeCoreXray), version)
	if err != nil {
		return corecatalog.Release{}, fmt.Errorf("%w: core_not_verified", domain.ErrValidation)
	}
	if release.RequiresConfirmation && !allowRestricted {
		return release, fmt.Errorf("%w: core_ack_required", domain.ErrValidation)
	}
	if release.Tier != corecatalog.TierRecommended && release.Tier != corecatalog.TierVerified && release.Tier != corecatalog.TierRestricted {
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
	recommended, err := corecatalog.Recommended(string(domain.NodeCoreXray))
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
	release, coreErr := ValidateCore(coreVersion, allowRestrictedReality)
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
		previous, normalizeErr := corecatalog.NormalizeVersion(snapshot.Panel.XrayVersion)
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
