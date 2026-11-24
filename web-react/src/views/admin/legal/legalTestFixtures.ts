import type * as legalApi from '@/api/legal'

// Fixtures shared by the tests of the Privacy and terms tab and of the document editor page. Not part of the app.
export const privacyText = '# Privacy\n\nThis service\n\n[[data-collection]]'
export const dc: legalApi.DataCollection = {
  sub_log: { enabled: true, retention_days: 0, includes_ip: true },
  dest_hits: { enabled: true, nodes: 4, retention_days: 30, includes_ip: false },
}
export const ver = (id: number, locale: string, version: number, at: string, bump = false) => ({ id, locale, version, consent_bump: bump, published_at: at, published_by: 1 })
const docOf = (v: ReturnType<typeof ver>, content: string) => ({ ...v, content })

export const terms: legalApi.AdminLegal = {
  kind: 'terms', enabled: true, consent_version: 3, data_collection: dc,
  documents: [docOf(ver(5, 'zh-CN', 4, '2026-09-30T08:00:00Z'), '# 条款'), docOf(ver(4, 'en-US', 2, '2026-09-12T08:00:00Z'), '# Terms')],
  versions: [ver(5, 'zh-CN', 4, '2026-09-30T08:00:00Z', true), ver(4, 'en-US', 2, '2026-09-12T08:00:00Z'), ver(2, 'zh-CN', 3, '2026-09-01T08:00:00Z')],
}
export const privacy: legalApi.AdminLegal = {
  kind: 'privacy', enabled: true, consent_version: 3, data_collection: dc,
  documents: [docOf(ver(3, 'zh-CN', 3, '2026-09-30T08:00:00Z'), privacyText)],
  versions: [ver(3, 'zh-CN', 3, '2026-09-30T08:00:00Z', true), ver(2, 'zh-CN', 2, '2026-09-20T08:00:00Z'), ver(1, 'zh-CN', 1, '2026-09-10T08:00:00Z')],
}
export const nothing = (kind: legalApi.LegalKind): legalApi.AdminLegal => ({ kind, enabled: false, consent_version: 0, documents: [], versions: [], data_collection: dc })
