package sqlstore

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

func destPolicyFromDomain(v domain.DestPolicy) destPolicyRow {
	return destPolicyRow{ID: v.ID, Name: v.Name, Action: string(v.Action), ListIDs: append(jsonInt64s(nil), v.ListIDs...), Inline: jsonDestInline(v.Inline), Scope: string(v.Scope), GroupIDs: append(jsonInt64s(nil), v.GroupIDs...), Priority: v.Priority, Enabled: v.Enabled, CountsAsRisk: v.CountsAsRisk, TemplateKey: v.TemplateKey, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func destPolicyToDomain(v destPolicyRow) domain.DestPolicy {
	inline := domain.DestInline(v.Inline)
	inline.CIDRs = append([]string(nil), inline.CIDRs...)
	inline.Protocols = append([]string(nil), inline.Protocols...)
	return domain.DestPolicy{ID: v.ID, Name: v.Name, Action: domain.DestAction(v.Action), ListIDs: append([]int64(nil), v.ListIDs...), Inline: inline, Scope: domain.DestScope(v.Scope), GroupIDs: append([]int64(nil), v.GroupIDs...), Priority: v.Priority, Enabled: v.Enabled, CountsAsRisk: v.CountsAsRisk, TemplateKey: v.TemplateKey, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func destListFromDomain(v domain.DestList) destListRow {
	return destListRow{ID: v.ID, Name: v.Name, Kind: string(v.Kind), SourceURL: v.SourceURL, GeositeCategory: v.GeositeCategory, GeositeAttrs: v.GeositeAttrs, ParseReport: destReportFromDomain(v.ParseReport), Entries: append(destBytes(nil), v.Entries...), SourceText: append(destBytes(nil), v.SourceText...), EntryCount: v.EntryCount, RegexpCount: v.RegexpCount, ContentSHA256: v.ContentSHA256, LastFetchedAt: v.LastFetchedAt, LastError: v.LastError, OwnerGroupID: v.OwnerGroupID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func destListToDomain(v destListRow) domain.DestList {
	return domain.DestList{ID: v.ID, Name: v.Name, Kind: domain.DestListKind(v.Kind), SourceURL: v.SourceURL, GeositeCategory: v.GeositeCategory, GeositeAttrs: v.GeositeAttrs, ParseReport: destReportToDomain(v.ParseReport), Entries: append([]byte(nil), v.Entries...), SourceText: append([]byte(nil), v.SourceText...), EntryCount: v.EntryCount, RegexpCount: v.RegexpCount, ContentSHA256: v.ContentSHA256, LastFetchedAt: v.LastFetchedAt, LastError: v.LastError, OwnerGroupID: v.OwnerGroupID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
