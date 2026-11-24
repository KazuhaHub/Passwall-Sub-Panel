import type { Block } from './blocks'
import type { Doc } from './parse'
import { fieldSpans } from './parse'
import { isIPv4, isIPv6 } from './validate'
import { KNOWN_TYPES, RULE_OPTIONS, SIMPLE_TYPES } from './types'

/**
 * Pasting a list (design 3.6): every line is read as whatever it most plausibly is, a rule line, a bare domain, an
 * address or range, a hosts line, an ad-filter line, a URL. The paste dialog shows the result for checking before
 * anything is written, so a guess that changes meaning is marked.
 */
export type BareDomainType = 'DOMAIN-SUFFIX' | 'DOMAIN'

export interface PasteEntry {
  type: string
  value: string
  options: string[]
  /** 'wildcard': `*.x` read as a suffix, which also matches `x` itself. 'url_host': the host name taken from a URL. */
  note?: 'wildcard' | 'url_host'
  /** 1-based line of the input. */
  line: number
}

export interface PasteScan {
  entries: PasteEntry[]
  /** Comment and blank lines skipped. */
  comments: number
  /** Rule lines that named a target of their own, which is dropped in favour of the group's. */
  ignoredTarget: number
  unrecognized: { line: number; text: string }[]
}

const DOMAIN_RE = /^(?=.{1,253}$)[^\s,/:#\\|^*+@?[\]()'"<>]+(\.[^\s,/:#\\|^*+@?[\]()'"<>]+)+$/u

function entry(type: string, value: string, options: string[], line: number, note?: PasteEntry['note']): PasteEntry {
  return note ? { type, value, options, note, line } : { type, value, options, line }
}

function readLine(text: string, line: number, bare: BareDomainType): { entry?: PasteEntry; ignoredTarget?: boolean; unrecognized?: boolean } {
  // A provider file lists its items as `- 'x'`.
  let s = text.trim().replace(/^-\s+/, '')
  s = s.replace(/^(['"])(.*)\1$/, '$2').trim()

  // A full rule line: its type is one the panel knows.
  const comma = s.indexOf(',')
  if (comma > 0) {
    const type = s.slice(0, comma).trim().toUpperCase()
    if (!KNOWN_TYPES.has(type) || !SIMPLE_TYPES.has(type)) return { unrecognized: true }
    const fields = fieldSpans(s).map(sp => s.slice(sp.start, sp.end).trim())
    const value = fields[1] ?? ''
    if (value === '') return { unrecognized: true }
    const rest = fields.slice(2).filter(f => f !== '')
    const options = rest.filter(f => RULE_OPTIONS.has(f.toLowerCase())).map(f => f.toLowerCase())
    const hasTarget = rest.some(f => !RULE_OPTIONS.has(f.toLowerCase()))
    return { entry: entry(type, value, options, line), ignoredTarget: hasTarget }
  }

  // Ad-filter syntax: ||example.com^
  const adblock = /^\|\|([^/^*|\s]+)\^?$/.exec(s)
  if (adblock && DOMAIN_RE.test(adblock[1])) return { entry: entry('DOMAIN-SUFFIX', adblock[1], [], line) }

  // A hosts line: an unspecified or loopback address, then a name.
  const hosts = /^(?:0\.0\.0\.0|127\.0\.0\.1|::1?)\s+(\S+)$/.exec(s)
  if (hosts && DOMAIN_RE.test(hosts[1])) return { entry: entry('DOMAIN-SUFFIX', hosts[1], [], line) }

  if (/^https?:\/\//i.test(s)) {
    try {
      const host = new URL(s).hostname
      if (DOMAIN_RE.test(host)) return { entry: entry('DOMAIN-SUFFIX', host, [], line, 'url_host') }
    } catch { /* not a URL */ }
    return { unrecognized: true }
  }

  if (isIPv4(s)) return { entry: entry('IP-CIDR', s.includes('/') ? s : `${s}/32`, ['no-resolve'], line) }
  if (isIPv6(s)) return { entry: entry('IP-CIDR6', s.includes('/') ? s : `${s}/128`, ['no-resolve'], line) }

  const wildcard = s.startsWith('*.')
  const plus = s.startsWith('+.') || (s.startsWith('.') && !s.startsWith('..'))
  const name = wildcard ? s.slice(2) : plus ? s.slice(s.startsWith('+.') ? 2 : 1) : s
  if (DOMAIN_RE.test(name) && !/^[\d.]+$/.test(name)) {
    if (wildcard) return { entry: entry('DOMAIN-SUFFIX', name, [], line, 'wildcard') }
    return { entry: entry(plus ? 'DOMAIN-SUFFIX' : bare, name, [], line) }
  }
  return { unrecognized: true }
}

export function scanPaste(text: string, bare: BareDomainType = 'DOMAIN-SUFFIX'): PasteScan {
  const out: PasteScan = { entries: [], comments: 0, ignoredTarget: 0, unrecognized: [] }
  const rows = text.split(/\r\n|\r|\n/)
  // The empty piece after a final newline is not a blank line the person wrote.
  if (rows.length > 0 && rows[rows.length - 1] === '') rows.pop()
  rows.forEach((raw, i) => {
    const s = raw.trim()
    if (s === '' || s.startsWith('#')) { out.comments++; return }
    if (/^payload:\s*$/i.test(s)) return
    const r = readLine(raw, i + 1, bare)
    if (r.entry) { out.entries.push(r.entry); if (r.ignoredTarget) out.ignoredTarget++ } else out.unrecognized.push({ line: i + 1, text: s })
  })
  return out
}

/** Domains match without regard to case; everything else exactly. */
const keyOf = (type: string, value: string) => {
  const t = type.toUpperCase()
  return `${t}|${t.startsWith('DOMAIN') || t.startsWith('GEOSITE') ? value.toLowerCase() : value}`
}

export type PasteTarget = { kind: 'into'; block: number } | { kind: 'new'; before: number }

export interface PasteReview {
  /** What would be written, in the order pasted. */
  add: PasteEntry[]
  duplicateInGroup: number
  duplicateInPaste: number
  /** Entries a group above already has: written, but they would never apply. */
  earlier: { value: string; block: number }[]
  /** Groups below that have rules the new entries would take over: per group, how many. */
  later: { block: number; count: number }[]
}

/**
 * Checks pasted entries against the document by exact type and value (the first-match semantics proper, a suffix
 * covering a longer name, is the phase-2 lint). `where` says where they will sit, which decides what counts as
 * "above" and "below".
 */
export function reviewPaste(entries: readonly PasteEntry[], doc: Doc, blocks: readonly Block[], where: PasteTarget): PasteReview {
  const seen = new Map<string, number[]>()
  for (const b of blocks) {
    if (b.kind !== 'rules') continue
    for (let i = b.ruleStart; i < b.end; i++) {
      const r = doc.lines[i].rule!
      const k = keyOf(r.type, r.value)
      const list = seen.get(k)
      if (!list) seen.set(k, [b.index])
      else if (list[list.length - 1] !== b.index) list.push(b.index)
    }
  }
  const dest = where.kind === 'into' ? where.block : -1
  const boundary = where.kind === 'into' ? where.block : where.before
  const paste = new Set<string>()
  const out: PasteReview = { add: [], duplicateInGroup: 0, duplicateInPaste: 0, earlier: [], later: [] }
  const later = new Map<number, number>()
  for (const e of entries) {
    const k = keyOf(e.type, e.value)
    if (paste.has(k)) { out.duplicateInPaste++; continue }
    paste.add(k)
    const where2 = seen.get(k) ?? []
    if (dest >= 0 && where2.includes(dest)) { out.duplicateInGroup++; continue }
    out.add.push(e)
    const above = where2.find(b => b < boundary)
    if (above !== undefined) out.earlier.push({ value: e.value, block: above })
    for (const b of where2) if (b > boundary || (where.kind === 'new' && b === boundary)) later.set(b, (later.get(b) ?? 0) + 1)
  }
  out.later = [...later].sort((a, b) => a[0] - b[0]).map(([block, count]) => ({ block, count }))
  return out
}
