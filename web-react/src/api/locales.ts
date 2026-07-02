// Admin CRUD for runtime-uploaded UI language packs. Uses the authenticated
// axios `client` (unlike the public bootstrap fetchers in ./i18n.ts). Writes are
// admin-only server-side (adminGroup) + gated in the UI on useCan('config.write').
import { client } from './client'
import type { LocaleMeta } from './i18n'

export type { LocaleMeta }

// Per-pack upload cap — mirrors maxLocalePackBytes in the backend handler
// (internal/transport/http/handler/admin_locales.go). Checked client-side too so
// the user gets a clear message instead of a raw 413.
export const MAX_LOCALE_PACK_BYTES = 512 * 1024

// LocalePack is the full upload shape (matches the on-disk JSON file + the
// backend localePackDTO). `psp_language_pack` is the format version.
export interface LocalePack {
  psp_language_pack: number
  code: string
  name: string
  author?: string
  base_language?: string
  base_version?: string
  namespaces: Record<string, Record<string, unknown>>
}

export async function listLocales(signal?: AbortSignal) {
  const { data } = await client.get<LocaleMeta[]>('/admin/locales', { signal })
  return data
}

export async function saveLocale(pack: LocalePack) {
  await client.put(`/admin/locales/${encodeURIComponent(pack.code)}`, pack)
}

export async function deleteLocale(code: string) {
  await client.delete(`/admin/locales/${encodeURIComponent(code)}`)
}
