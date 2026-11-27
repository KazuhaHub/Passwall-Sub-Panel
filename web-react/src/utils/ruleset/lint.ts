import type { Continuation } from './continuation'
import type { Doc } from './parse'
import { cidrOf } from './match'

/**
 * Rules that can never apply because an earlier rule already catches everything they would (design 3.13). The first rule that
 * matches decides, so a later rule covered by an earlier one is dead weight at best (same target) and a silent mistake at worst
 * (a different target: what the author meant is not what happens).
 *
 * "Covered" is decided only where it is exact:
 *   - the same rule again (same type, value and options), whatever its target;
 *   - DOMAIN-SUFFIX s covers DOMAIN d and DOMAIN-SUFFIX s2 when d / s2 is s or ends in `.s`;
 *   - DOMAIN-KEYWORD k covers a DOMAIN, DOMAIN-SUFFIX or DOMAIN-KEYWORD whose text contains k;
 *   - DOMAIN d covers only DOMAIN d;
 *   - an address range covers a range inside it of the same family, if it also catches what the narrower one catches: a wide
 *     rule that does not resolve names covers a narrow one that does not either way, and one that resolves covers both, but a
 *     wide `no-resolve` rule does not cover a narrow one that resolves (that one also matches names after DNS);
 *   - anything after a MATCH (one that does not name PASS).
 * A rule whose target may not be the last word covers nothing: PASS, a Rematch outbound, or a group whose members can lead to PASS
 * (Mihomo goes on to the next rule then; see ./continuation, the one predicate the routing test uses too). A rule with no target, or one the editor
 * reads only as text, neither covers nor is reported. Regular expressions, GeoIP and the like are never compared.
 *
 * How it stays fast (the design's budget is 50,000 rules in 100 ms): domain suffixes live in a trie of reversed labels, so a rule
 * is checked against every suffix above it in one walk down its own labels; exact domains in a map; keywords in a short list;
 * address ranges in one map per prefix length. A rule that is itself covered is not added, since whatever it would cover its own
 * coverer covers too, and the report then points at a rule that actually applies.
 */

export type ShadowKind = 'same' | 'other' | 'after_match'
export interface Finding {
  /** The covered rule's line (from 0) and the first rule that covers it. */
  line: number
  by: number
  kind: ShadowKind
  /** The covering rule is the same rule written again (not only a broader one). */
  duplicate: boolean
}

interface TrieNode { kids: Map<string, TrieNode>; first: number }
const node = (): TrieNode => ({ kids: new Map(), first: -1 })

interface Ranges {
  /** [family][prefix length] -> (network prefix as text -> first line). One set for rules that resolve names, one for no-resolve. */
  resolving: Map<number, Map<string, number>>[]
  literal: Map<number, Map<string, number>>[]
}

const DOMAIN_KINDS = new Set(['DOMAIN', 'DOMAIN-SUFFIX', 'DOMAIN-KEYWORD'])
const ADDRESS_KINDS = new Set(['IP-CIDR', 'IP-CIDR6'])

// Options are exact (Mihomo's ParseParams uses slices.Contains): `NO-RESOLVE` is not `no-resolve`, so it neither makes a rule literal-only
// nor makes two rules the same.
const has = (o: readonly string[], name: string) => o.includes(name)

export function findShadowed(doc: Doc, cont: Continuation): Finding[] {
  // A line the editor cannot place in the list makes every statement about order unreliable.
  if (doc.foreign.length > 0) return []
  const out: Finding[] = []
  let terminal = -1
  const exactDomain = new Map<string, number>()
  const suffix = node()
  const keywords: { k: string; idx: number }[] = []
  // Every rule once, by what it matches: the first line, its target, and whether it is the last word where it matches.
  const seen = new Map<string, { idx: number; target: string; last: boolean }>()
  const ranges: Ranges = { resolving: [new Map(), new Map()], literal: [new Map(), new Map()] }

  doc.lines.forEach((line, i) => {
    const rule = line.rule
    if (line.kind !== 'rule' || !rule) return
    const type = rule.type.toUpperCase()
    if (terminal >= 0) { out.push({ line: i, by: terminal, kind: 'after_match', duplicate: false }); return }
    if (rule.target === '') return
    // A target that may carry on matching (PASS, a group that can reach PASS, a Rematch outbound) is not the last word.
    const passes = cont.mayContinue(rule.target)
    if (type === 'MATCH') { if (!passes) terminal = i; return }

    const value = DOMAIN_KINDS.has(type) ? rule.value.toLowerCase() : rule.value
    const options = [...rule.options].sort()
    const key = `${type}\u0000${value}\u0000${options.join(',')}`
    let by = -1
    let duplicate = false
    const consider = (idx: number | undefined, dup = false) => {
      if (idx === undefined || idx < 0) return
      if (by < 0 || idx < by) { by = idx; duplicate = dup } else if (idx === by && dup) duplicate = true
    }

    // The same rule again covers this one if the earlier is the last word, or if both lead to the same target (whatever that does
    // next, it does it again): so a duplicate aimed at one group that may pass is still redundant, one aimed elsewhere is not.
    const earlier = seen.get(key)
    if (earlier && (earlier.last || earlier.target === rule.target)) consider(earlier.idx, true)
    if (type === 'DOMAIN') {
      consider(exactDomain.get(value))
      walk(suffix, value, n => consider(n.first))
      for (const kw of keywords) if (value.includes(kw.k)) { consider(kw.idx); break }
    } else if (type === 'DOMAIN-SUFFIX') {
      walk(suffix, value, n => consider(n.first))
      for (const kw of keywords) if (value.includes(kw.k)) { consider(kw.idx); break }
    } else if (type === 'DOMAIN-KEYWORD') {
      for (const kw of keywords) if (value.includes(kw.k)) { consider(kw.idx); break }
    } else if (ADDRESS_KINDS.has(type) && !has(rule.options, 'src')) {
      const cidr = cidrOf(value)
      if (cidr) {
        const literalOnly = has(rule.options, 'no-resolve')
        const fam = cidr.v === 4 ? 0 : 1
        const bits = cidr.v === 4 ? 32 : 128
        for (let len = 0; len <= cidr.len; len++) {
          const prefix = (cidr.n >> BigInt(bits - len)).toString()
          consider(ranges.resolving[fam].get(len)?.get(prefix))
          if (literalOnly) consider(ranges.literal[fam].get(len)?.get(prefix))
        }
      }
    }

    if (by >= 0) {
      const covering = doc.lines[by].rule!
      out.push({ line: i, by, kind: covering.target === rule.target ? 'same' : 'other', duplicate })
      return
    }
    const known = seen.get(key)
    if (!known || (!known.last && !passes)) seen.set(key, { idx: i, target: rule.target, last: !passes })
    if (passes) return
    // Not covered: it can cover later rules.
    if (type === 'DOMAIN') exactDomain.set(value, exactDomain.get(value) ?? i)
    else if (type === 'DOMAIN-SUFFIX') {
      const n = labels(value).reduce((cur, label) => { let next = cur.kids.get(label); if (!next) { next = node(); cur.kids.set(label, next) } return next }, suffix)
      if (n.first < 0) n.first = i
    } else if (type === 'DOMAIN-KEYWORD') keywords.push({ k: value, idx: i })
    else if (ADDRESS_KINDS.has(type) && !has(rule.options, 'src')) {
      const cidr = cidrOf(value)
      if (cidr) {
        const fam = cidr.v === 4 ? 0 : 1
        const bits = cidr.v === 4 ? 32 : 128
        const store = has(rule.options, 'no-resolve') ? ranges.literal[fam] : ranges.resolving[fam]
        let byLen = store.get(cidr.len)
        if (!byLen) { byLen = new Map(); store.set(cidr.len, byLen) }
        const prefix = (cidr.n >> BigInt(bits - cidr.len)).toString()
        if (!byLen.has(prefix)) byLen.set(prefix, i)
      }
    }
  })
  return out
}

/** Labels from the top of the name down: `www.example.com` is com, example, www. */
const labels = (name: string) => name.split('.').reverse()

/** Calls `visit` for every trie node on the way down `name`'s labels (each is a suffix rule that covers the name, if it has one). */
function walk(root: TrieNode, name: string, visit: (n: TrieNode) => void) {
  let cur = root
  for (const label of labels(name)) {
    const next = cur.kids.get(label)
    if (!next) return
    cur = next
    if (cur.first >= 0) visit(cur)
  }
}

export interface ShadowSummary { same: number; other: number; afterMatch: number; total: number }
export function summarize(findings: readonly Finding[]): ShadowSummary {
  let same = 0
  let other = 0
  let afterMatch = 0
  for (const f of findings) { if (f.kind === 'same') same++; else if (f.kind === 'other') other++; else afterMatch++ }
  return { same, other, afterMatch, total: findings.length }
}
