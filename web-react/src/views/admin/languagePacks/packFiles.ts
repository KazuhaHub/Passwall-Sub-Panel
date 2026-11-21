import { fetchLanguageBundle } from '@/api/i18n'
import type { LocaleMeta, LocalePack } from '@/api/locales'
import { LANGUAGE_PACK_FORMAT, loadBuiltinSource } from '@/i18n'
import { getVersion } from '@/api/version'

/** Saves `value` as a JSON file through a temporary link. */
export function downloadJson(filename: string, value: unknown) {
  const blob = new Blob([JSON.stringify(value, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

/**
 * The shipped en-US strings as a fill-in-the-blank pack, so a translator starts from the exact current
 * keys. Entirely client-side: the base lives in the JS bundle, not the backend.
 */
export async function exportBaseTemplate() {
  const namespaces = await loadBuiltinSource('en-US')
  let baseVersion = ''
  try { baseVersion = (await getVersion()).version } catch { /* version is advisory */ }
  const pack: LocalePack = {
    psp_language_pack: LANGUAGE_PACK_FORMAT,
    code: '',
    name: '',
    author: '',
    base_language: 'en-US',
    base_version: baseVersion,
    namespaces: namespaces as Record<string, Record<string, unknown>>,
  }
  downloadJson('psp-language-pack-base-en-US.json', pack)
}

/**
 * An uploaded pack as the file that could be uploaded again: its manifest row plus the strings the
 * public endpoint serves (the manifest carries no strings, and the public bundle no names).
 */
export async function downloadPack(meta: LocaleMeta) {
  const bundle = await fetchLanguageBundle(meta.code)
  const pack: LocalePack = {
    psp_language_pack: LANGUAGE_PACK_FORMAT,
    code: meta.code,
    name: meta.name,
    author: meta.author ?? '',
    base_version: meta.base_version ?? '',
    namespaces: bundle.namespaces,
  }
  downloadJson(`psp-language-pack-${meta.code}.json`, pack)
}
