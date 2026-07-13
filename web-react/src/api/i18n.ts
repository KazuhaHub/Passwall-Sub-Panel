// Public i18n endpoints for runtime-uploaded language packs. These run on the
// pre-mount bootstrap path (before React, before auth), so they use the raw
// fetch API rather than the axios `client` — importing `client` here would pull
// in the auth/i18n graph and create a circular import with @/i18n.
import { panelAPIBase } from '@/panelPath'

// LocaleMeta mirrors the backend manifest row (no translation bodies).
export interface LocaleMeta {
  code: string
  name: string
  author?: string
  base_version?: string
  etag?: string
}

export interface LocaleBundle {
  // namespace -> nested translation object (flattened by the caller at register time)
  namespaces: Record<string, Record<string, unknown>>
}

// fetchLanguages returns the manifest of uploaded packs (the two built-ins are
// compiled in and are not listed here).
export async function fetchLanguages(): Promise<LocaleMeta[]> {
  const res = await fetch(`${panelAPIBase}/i18n/langs`, { headers: { Accept: 'application/json' } })
  if (!res.ok) throw new Error(`GET /api/i18n/langs -> ${res.status}`)
  return res.json()
}

// fetchLanguageBundle returns one pack's translation namespaces.
export async function fetchLanguageBundle(code: string): Promise<LocaleBundle> {
  const res = await fetch(`${panelAPIBase}/i18n/${encodeURIComponent(code)}`, { headers: { Accept: 'application/json' } })
  if (!res.ok) throw new Error(`GET /api/i18n/${code} -> ${res.status}`)
  return res.json()
}
