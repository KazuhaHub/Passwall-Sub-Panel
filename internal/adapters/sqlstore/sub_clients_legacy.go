package sqlstore

// ─────────────────────────────────────────────────────────────────────────
// ONE-TIME BACKWARD COMPATIBILITY — Remove in the next major (v4.0.0)
//
// v3.3.0 unified the two legacy subscription-client settings tables
// (`sub_client_rules` + `sub_import_clients`) into a single nested registry
// (`sub_clients`, the "family → app" model). Panels upgrading from <= v3.2.1
// have data only under the old keys. This file folds that legacy data into
// the new shape on first load (kvSettingsRepo.applyUISettingsDefaults), so the
// admin doesn't lose their customized client config.
//
// It is deliberately isolated in its own file and references only the legacy
// structs; once every panel has loaded once under v3.2.x (which rewrites the
// settings as `sub_clients`), this whole file — plus the deprecated
// SubClientRules / SubImportClients fields and their KV descriptors — can be
// deleted. Tracked in CHANGELOG / docs/ARCHITECTURE.md.
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// migrateLegacySubClients folds the legacy detection rules + import clients
// into the unified family→app registry. Returns nil when there's no legacy
// data to migrate (so the caller falls back to fresh defaults).
//
// Families come from the detection rules (verbatim). Each import client is
// attached to the family it belongs to, matched first by "same render format
// AND the import name contains one of the family's UA keywords" (so V2rayNG
// lands on the V2rayNG family, not V2RayN), then by first same-format family,
// and finally — if no family serves that format — under a new bare family so
// the app is never dropped. The render format is the family's; apps shed it.
func migrateLegacySubClients(rules []ports.SubClientRule, imports []ports.SubImportClient) []ports.SubClientFamily {
	if len(rules) == 0 && len(imports) == 0 {
		return nil
	}
	families := make([]ports.SubClientFamily, 0, len(rules))
	for _, r := range rules {
		families = append(families, ports.SubClientFamily{
			Name:         r.Name,
			Keywords:     r.Keywords,
			RenderFormat: r.RenderFormat,
			Enabled:      r.Enabled,
		})
	}
	for _, imp := range imports {
		idx := matchLegacyFamily(families, imp)
		if idx < 0 {
			families = append(families, ports.SubClientFamily{
				Name:         imp.Name,
				RenderFormat: imp.RenderFormat,
				Enabled:      true,
			})
			idx = len(families) - 1
		}
		families[idx].Apps = append(families[idx].Apps, ports.SubClientApp{
			Name:              imp.Name,
			Platforms:         imp.Platforms,
			ImportURLTemplate: imp.ImportURLTemplate,
			InstallURL:        imp.InstallURL,
			Enabled:           imp.Enabled,
			Sort:              imp.Sort,
			RecommendedFor:    imp.RecommendedFor,
		})
	}
	return families
}

func matchLegacyFamily(families []ports.SubClientFamily, imp ports.SubImportClient) int {
	name := strings.ToLower(imp.Name)
	for i := range families {
		if families[i].RenderFormat != imp.RenderFormat {
			continue
		}
		for _, kw := range families[i].Keywords {
			if kw != "" && strings.Contains(name, strings.ToLower(kw)) {
				return i
			}
		}
	}
	for i := range families {
		if families[i].RenderFormat == imp.RenderFormat {
			return i
		}
	}
	return -1
}
