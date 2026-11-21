import { LANGUAGE_PACK_FORMAT, isBuiltinLanguage } from '@/i18n'
import { NAMESPACES } from '@/i18n/options'
import { MAX_LOCALE_PACK_BYTES, type LocalePack } from '@/api/locales'

/** One thing wrong with a pack, as an admin-namespace key and its values. */
export interface PackProblem { key: string; values?: Record<string, unknown> }

export interface PackCheck {
  /** The parsed pack; present whenever the text parsed to an object, even with problems, so the summary can still name it. */
  pack: LocalePack | null
  problems: PackProblem[]
}

/** How many unusable values are listed before the rest are counted. */
const LEAF_LIMIT = 5
// The same shape the server accepts for a file name (service/locale validate.go codePattern).
const CODE_PATTERN = /^[A-Za-z0-9_-]+$/

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const kindOf = (v: unknown) => v === null ? 'null' : Array.isArray(v) ? 'array' : typeof v

/**
 * Every problem the server would refuse the text for, found before anything is sent, so an admin
 * sees all of them at once instead of one per attempt. The server validates again and stays the
 * authority (service/locale.Validate): this mirrors its rules (format version, code, name, the
 * closed namespace set, string-only leaves) plus the size cap the handler enforces.
 */
export function checkPack(text: string): PackCheck {
  if (text.trim() === '') return { pack: null, problems: [] }
  if (new Blob([text]).size > MAX_LOCALE_PACK_BYTES) {
    return { pack: null, problems: [{ key: 'too_large', values: { max: Math.round(MAX_LOCALE_PACK_BYTES / 1024) } }] }
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch (err) {
    return { pack: null, problems: [{ key: 'invalid_json', values: { detail: err instanceof Error ? err.message : '' } }] }
  }
  if (!isObject(parsed)) return { pack: null, problems: [{ key: 'not_object' }] }

  const problems: PackProblem[] = []
  if (parsed.psp_language_pack !== LANGUAGE_PACK_FORMAT) {
    problems.push({ key: 'format', values: { found: String(parsed.psp_language_pack ?? '—'), expected: LANGUAGE_PACK_FORMAT } })
  }
  const code = typeof parsed.code === 'string' ? parsed.code : ''
  if (!code) problems.push({ key: 'code_required' })
  else if (!CODE_PATTERN.test(code)) problems.push({ key: 'code_chars', values: { code } })
  else if (isBuiltinLanguage(code)) problems.push({ key: 'code_builtin', values: { code } })
  if (typeof parsed.name !== 'string' || !parsed.name) problems.push({ key: 'name_required' })

  const spaces = parsed.namespaces
  if (!isObject(spaces)) {
    problems.push({ key: 'namespaces_required' })
  } else {
    let unusable = 0
    const walk = (path: string, v: unknown) => {
      if (typeof v === 'string') return
      if (isObject(v)) { for (const [k, child] of Object.entries(v)) walk(`${path}.${k}`, child); return }
      if (++unusable <= LEAF_LIMIT) problems.push({ key: 'leaf', values: { path, kind: kindOf(v) } })
    }
    for (const [ns, tree] of Object.entries(spaces)) {
      if (!(NAMESPACES as readonly string[]).includes(ns)) { problems.push({ key: 'unknown_namespace', values: { ns, known: NAMESPACES.join(', ') } }); continue }
      if (!isObject(tree)) { problems.push({ key: 'namespace_shape', values: { ns } }); continue }
      for (const [k, child] of Object.entries(tree)) walk(`${ns}:${k}`, child)
    }
    if (unusable > LEAF_LIMIT) problems.push({ key: 'leaf_more', values: { count: unusable - LEAF_LIMIT } })
  }
  return { pack: parsed as unknown as LocalePack, problems }
}
