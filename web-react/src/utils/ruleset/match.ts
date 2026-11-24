import type { Continuation } from './continuation'
import { splitFields, type Doc, type RuleInfo } from './parse'

/**
 * "Test routing" (design 3.9): which rule of the DRAFT a connection meets first. A browser can decide some rule types and not
 * others, and this file is where that line is drawn. Three answers, never two:
 *
 *   yes      the rule matches this connection;
 *   no       it does not;
 *   unknown  it cannot be decided here (it needs the client's GeoIP / GeoSite data, a name resolved over DNS, an input that was
 *            not given, a regular expression that could not be run safely, a type this tester does not know), and the reason.
 *
 * An `unknown` line may match, so everything after it is uncertain: `routeFor` reports the first rule that DOES match and, next
 * to it, the undecidable lines above it that could have matched first. Nothing here ever turns an unknown into a no.
 *
 * Match semantics are the ones the design documents (1.3 to 1.5, 3.6, 3.9): suffix rules also match the domain itself, keyword is
 * a substring, `no-resolve` stops a domain being resolved to test an address rule, a connection to an IP address has no domain.
 * What the design does not state is not guessed: it comes back `unknown`.
 */

export interface Ip { v: 4 | 6; n: bigint }

/** What was typed in the tester, as the form holds it. */
export interface ProbeInput {
  address: string
  port: string
  network: 'tcp' | 'udp'
  /** The address the domain resolves to (optional). */
  resolvedIp: string
  process: string
  srcPort: string
  srcIp: string
}

export interface Probe {
  /** A connection to a domain, or to an IP address (which has no domain). */
  kind: 'domain' | 'ip'
  host: string
  ip: Ip | null
  port: number | null
  network: 'tcp' | 'udp'
  resolved: Ip | null
  process: string
  srcPort: number | null
  srcIp: Ip | null
  /** Set while walking a list: an earlier rule has already made Mihomo resolve the name, so the address is known from here on. */
  resolvedEarlier?: boolean
}

export type ProbeProblem = 'address_empty' | 'address_invalid' | 'port_invalid' | 'resolved_invalid' | 'src_invalid'

export function parseIp(text: string): Ip | null {
  const s = text.trim()
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(s)) {
    const parts = s.split('.').map(Number)
    if (parts.some(p => p > 255)) return null
    return { v: 4, n: BigInt(((parts[0] * 256 + parts[1]) * 256 + parts[2]) * 256 + parts[3]) }
  }
  if (!s.includes(':') || !/^[0-9a-fA-F:.]+$/.test(s)) return null
  let groups = s
  // An address may end in a dotted IPv4 (::ffff:1.2.3.4), which is the last two groups.
  const tail = /(?:^|:)(\d{1,3}(?:\.\d{1,3}){3})$/.exec(s)
  if (tail) {
    const v4 = parseIp(tail[1])
    if (!v4) return null
    const hi = (v4.n >> 16n).toString(16)
    const lo = (v4.n & 0xffffn).toString(16)
    groups = s.slice(0, s.length - tail[1].length) + `${hi}:${lo}`
  }
  if (groups.includes('.')) return null
  const halves = groups.split('::')
  if (halves.length > 2) return null
  const side = (x: string) => (x === '' ? [] : x.split(':'))
  const head = side(halves[0])
  const rest = halves.length === 2 ? side(halves[1]) : []
  if (halves.length === 1 ? head.length !== 8 : head.length + rest.length > 7) return null
  const all = halves.length === 1 ? head : [...head, ...Array(8 - head.length - rest.length).fill('0'), ...rest]
  let n = 0n
  for (const g of all) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(g)) return null
    n = (n << 16n) | BigInt(parseInt(g, 16))
  }
  return { v: 6, n }
}

const portOf = (s: string): number | null => (/^\d{1,5}$/.test(s.trim()) && Number(s) <= 65535 ? Number(s) : null)

export function prepareProbe(input: ProbeInput): { ok: true; probe: Probe } | { ok: false; problem: ProbeProblem } {
  let host = input.address.trim().toLowerCase()
  if (host === '') return { ok: false, problem: 'address_empty' }
  if (host.startsWith('[') && host.endsWith(']')) host = host.slice(1, -1)
  if (host.endsWith('.') && !host.includes(':')) host = host.slice(0, -1)
  // A domain name is at most 253 characters, which also bounds what a regular expression is ever run against.
  if (host.length > 253) return { ok: false, problem: 'address_invalid' }
  const ip = parseIp(host)
  // A host name has no spaces, commas, slashes or scheme; what is typed is a host, not a URL.
  if (!ip && (host === '' || /[\s,/\\:@?#]/.test(host))) return { ok: false, problem: 'address_invalid' }
  let port: number | null = null
  if (input.port.trim() !== '') {
    port = portOf(input.port)
    if (port === null) return { ok: false, problem: 'port_invalid' }
  }
  let srcPort: number | null = null
  if (input.srcPort.trim() !== '') {
    srcPort = portOf(input.srcPort)
    if (srcPort === null) return { ok: false, problem: 'src_invalid' }
  }
  const resolved = input.resolvedIp.trim() === '' ? null : parseIp(input.resolvedIp)
  if (input.resolvedIp.trim() !== '' && !resolved) return { ok: false, problem: 'resolved_invalid' }
  const srcIp = input.srcIp.trim() === '' ? null : parseIp(input.srcIp)
  if (input.srcIp.trim() !== '' && !srcIp) return { ok: false, problem: 'src_invalid' }
  return { ok: true, probe: { kind: ip ? 'ip' : 'domain', host, ip, port, network: input.network, resolved, process: input.process.trim(), srcPort, srcIp } }
}

export type UnknownReason = 'client_data' | 'resolve' | 'input' | 'regex' | 'sub_rule' | 'unsupported'
export type Verdict = { verdict: 'yes' } | { verdict: 'no' } | { verdict: 'unknown'; reason: UnknownReason }
const YES: Verdict = { verdict: 'yes' }
const NO: Verdict = { verdict: 'no' }
const unknown = (reason: UnknownReason): Verdict => ({ verdict: 'unknown', reason })
const of = (b: boolean): Verdict => (b ? YES : NO)

// ---- regular expressions -------------------------------------------------------------------------------------------

/**
 * A regular expression from a rule, as a JavaScript RegExp, or null when it should not be run here. Mihomo compiles DOMAIN-REGEX
 * with regexp2 (a .NET-style engine) ignoring case; this runs the pattern with JavaScript's engine and the `i` flag, which is
 * only the same thing for a conservative subset: literals, `.`, `^` `$`, character classes (no nesting, no set operations, no
 * empty class), the escapes `\d \w \s \D \W \S` and escaped punctuation, `* + ? {n} {n,} {n,m}` (greedy only), alternation and
 * plain or `(?:` groups. Anything else (`\A \Z \z \G \b`, named groups, look-around, back-references, inline flags, lazy or
 * possessive quantifiers, Unicode classes, `[^]`, ...) is read differently by the two engines and is refused, so the answer is
 * "undecided" instead of a wrong "no".
 *
 * Nor is a pattern run when it could hang the page: nothing quantified that holds a quantifier or an alternation (nested
 * quantifiers), and at most two unbounded quantifiers (`*`, `+`, `{n,}`), which also keeps polynomial blow-up out. A browser
 * cannot interrupt a regular expression that is already running, so refusing before it starts is the only guard there is; the
 * test input is a host name of at most 253 characters.
 */
export function safeRegex(src: string): RegExp | null {
  if (src === '' || src.length > 200) return null
  const PUNCT = '.^$*+?()[]{}|\\/-'
  const escapeOk = (e: string | undefined) => e !== undefined && ('dwsDWS'.includes(e) || PUNCT.includes(e))
  const n = src.length
  // For each open group: whether it holds a quantifier or an alternation.
  const stack: boolean[] = []
  let unbounded = 0
  // The previous token can take a quantifier (an atom or a closed group); a second one in a row would be lazy, possessive or invalid.
  let atom = false
  const mark = () => { if (stack.length > 0) stack[stack.length - 1] = true }
  for (let i = 0; i < n;) {
    const ch = src[i]
    if (ch === '\\') {
      if (!escapeOk(src[i + 1])) return null
      i += 2; atom = true; continue
    }
    if (ch === '[') {
      i++
      if (src[i] === '^') i++
      let members = 0
      while (i < n && src[i] !== ']') {
        if (src[i] === '[') return null
        if (src[i] === '\\') { if (!escapeOk(src[i + 1])) return null; i += 2 } else i++
        members++
      }
      if (i >= n || members === 0) return null
      i++; atom = true; continue
    }
    if (ch === '(') {
      if (src[i + 1] === '?') { if (src[i + 2] !== ':') return null; i += 3 } else i++
      stack.push(false); atom = false; continue
    }
    if (ch === ')') {
      const inner = stack.pop()
      if (inner === undefined) return null
      const next = src[i + 1]
      if (inner && (next === '*' || next === '+' || next === '{')) return null
      if (inner) mark()
      i++; atom = true; continue
    }
    if (ch === '|') { mark(); i++; atom = false; continue }
    if (ch === '*' || ch === '+' || ch === '?') {
      if (!atom) return null
      if (ch !== '?') unbounded++
      mark(); i++; atom = false; continue
    }
    if (ch === '{') {
      const m = /^\{(\d{1,4})(,(\d{0,4}))?\}/.exec(src.slice(i))
      if (!m || !atom) return null
      if (m[2] !== undefined && m[3] === '') unbounded++
      else if (m[3] !== undefined && m[3] !== '' && Number(m[3]) < Number(m[1])) return null
      mark(); i += m[0].length; atom = false; continue
    }
    if (ch === '}') return null
    if (ch === '^' || ch === '$') { i++; atom = false; continue }
    i++; atom = true
  }
  if (stack.length > 0 || unbounded > 2) return null
  try { return new RegExp(src, 'i') } catch { return null }
}

const regexCache = new Map<string, RegExp | null>()
function regexFor(src: string): RegExp | null {
  if (regexCache.has(src)) return regexCache.get(src)!
  if (regexCache.size > 5000) regexCache.clear()
  const re = safeRegex(src)
  regexCache.set(src, re)
  return re
}

// ---- address ranges --------------------------------------------------------------------------------------------------

interface Cidr { v: 4 | 6; n: bigint; len: number }
const cidrCache = new Map<string, Cidr | null>()
function cidrOf(text: string): Cidr | null {
  const hit = cidrCache.get(text)
  if (hit !== undefined) return hit
  if (cidrCache.size > 20000) cidrCache.clear()
  let out: Cidr | null = null
  const m = /^([^/]+)\/(\d{1,3})$/.exec(text.trim())
  if (m) {
    const ip = parseIp(m[1])
    const len = Number(m[2])
    if (ip && len <= (ip.v === 4 ? 32 : 128)) out = { v: ip.v, n: ip.n, len }
  }
  cidrCache.set(text, out)
  return out
}
const inCidr = (c: Cidr, ip: Ip) => {
  if (c.v !== ip.v) return false
  const shift = BigInt((c.v === 4 ? 32 : 128) - c.len)
  return c.n >> shift === ip.n >> shift
}

// ---- one rule --------------------------------------------------------------------------------------------------------------

const GEO_DATA = new Set(['GEOIP', 'GEOSITE', 'RULE-SET', 'IP-ASN', 'SRC-GEOIP', 'SRC-IP-ASN'])
const hasOption = (options: readonly string[], name: string) => options.some(o => o.toLowerCase() === name)

function portRange(value: string): [number, number] | null {
  const m = /^(\d{1,5})(?:-(\d{1,5}))?$/.exec(value.trim())
  if (!m) return null
  const a = Number(m[1])
  const b = m[2] === undefined ? a : Number(m[2])
  return a <= 65535 && b <= 65535 && a <= b ? [a, b] : null
}

function addressRule(value: string, options: readonly string[], source: boolean, p: Probe): Verdict {
  const net = cidrOf(value)
  if (!net) return unknown('unsupported')
  if (source) return p.srcIp ? of(inCidr(net, p.srcIp)) : unknown('input')
  if (p.kind === 'ip') return of(inCidr(net, p.ip!))
  // A domain: the address is only known if it is resolved, and `no-resolve` is the rule's promise not to do that for it.
  if (hasOption(options, 'no-resolve')) {
    // Skipped without resolving, unless an earlier rule already resolved the name: then Mihomo has the address and uses it.
    if (p.resolvedEarlier) return p.resolved ? of(inCidr(net, p.resolved)) : unknown('resolve')
    // On its own a rule cannot know whether an earlier one resolved it; if the resolved address would fit, that decides.
    return p.resolved && inCidr(net, p.resolved) ? unknown('resolve') : NO
  }
  return p.resolved ? of(inCidr(net, p.resolved)) : unknown('resolve')
}

const MAX_DEPTH = 6

function logical(type: string, value: string, p: Probe, depth: number): Verdict {
  const text = value.trim()
  if (depth > MAX_DEPTH || !text.startsWith('(') || !text.endsWith(')')) return unknown('unsupported')
  const parts = splitFields(text.slice(1, -1))
  const subs: Verdict[] = []
  for (const part of parts) {
    if (!part.startsWith('(') || !part.endsWith(')')) return unknown('unsupported')
    const fields = splitFields(part.slice(1, -1))
    if (fields.length < 2 || fields[0] === '') return unknown('unsupported')
    subs.push(evalAtom(fields[0], fields[1], fields.slice(2), p, depth + 1))
  }
  if (subs.length === 0) return unknown('unsupported')
  const firstUnknown = subs.find(s => s.verdict === 'unknown')
  if (type === 'NOT') {
    if (subs.length !== 1) return unknown('unsupported')
    return subs[0].verdict === 'unknown' ? subs[0] : of(subs[0].verdict === 'no')
  }
  if (type === 'AND') {
    if (subs.some(s => s.verdict === 'no')) return NO
    return firstUnknown ?? YES
  }
  if (subs.some(s => s.verdict === 'yes')) return YES
  return firstUnknown ?? NO
}

function evalAtom(type: string, value: string, options: readonly string[], p: Probe, depth: number): Verdict {
  const t = type.toUpperCase()
  const v = value.trim()
  switch (t) {
    case 'MATCH': return YES
    case 'DOMAIN': return p.kind === 'ip' ? NO : of(p.host === v.toLowerCase())
    case 'DOMAIN-SUFFIX': {
      if (p.kind === 'ip') return NO
      const s = v.toLowerCase()
      return of(p.host === s || p.host.endsWith('.' + s))
    }
    case 'DOMAIN-KEYWORD': return p.kind === 'ip' ? NO : of(p.host.includes(v.toLowerCase()))
    case 'DOMAIN-REGEX': {
      if (p.kind === 'ip') return NO
      const re = regexFor(v)
      return re ? of(re.test(p.host)) : unknown('regex')
    }
    case 'IP-CIDR': case 'IP-CIDR6': return addressRule(v, options, hasOption(options, 'src'), p)
    case 'SRC-IP-CIDR': return addressRule(v, options, true, p)
    case 'DST-PORT': case 'SRC-PORT': {
      const range = portRange(v)
      if (!range) return unknown('unsupported')
      const have = t === 'DST-PORT' ? p.port : p.srcPort
      return have === null ? unknown('input') : of(have >= range[0] && have <= range[1])
    }
    case 'NETWORK': return /^(tcp|udp)$/i.test(v) ? of(v.toLowerCase() === p.network) : unknown('unsupported')
    case 'PROCESS-NAME': {
      if (p.process === '') return unknown('input')
      if (p.process === v) return YES
      // Whether the client compares the name case-sensitively is not stated, so a name that differs only in case is not decided.
      return p.process.toLowerCase() === v.toLowerCase() ? unknown('input') : NO
    }
    case 'AND': case 'OR': case 'NOT': return logical(t, value, p, depth)
    case 'SUB-RULE': case 'REMATCH-NAME': return unknown('sub_rule')
    default: return GEO_DATA.has(t) ? unknown('client_data') : unknown('unsupported')
  }
}

export function evalRule(rule: RuleInfo, p: Probe): Verdict {
  return evalAtom(rule.type, rule.value, rule.options, p, 0)
}

// ---- the whole list ----------------------------------------------------------------------------------------------------------

export interface RouteHit { line: number; target: string }
export interface MaybeHit extends RouteHit { reason: UnknownReason }

export interface RouteResult {
  /** The first rule that matches, with its line (counted from 0, as the document is); null when no rule decidably matches. */
  first: RouteHit | null
  /** `first` is the last word: its target is a built-in outbound or a group that cannot lead to PASS or a Rematch outbound. When it is not, matching may go on below it. */
  final: boolean
  /** The answer is the answer: nothing undecided stands above `first` and `first` is final. */
  certain: boolean
  /** Lines above `first` (all of them when there is none) that could not be decided and may match first. Capped; see `maybeTotal`. */
  maybe: MaybeHit[]
  maybeTotal: number
  /** Rules below `first` that also match. When `first` is final they never apply; when it is not, they may. Capped; see `laterTotal`. */
  later: RouteHit[]
  laterTotal: number
  /** Rules that match but name PASS, which skips them and carries on (Mihomo only), above `first`. */
  passed: RouteHit[]
}

const CAP = 100
const RESOLVES = new Set(['IP-CIDR', 'IP-CIDR6', 'IP-SUFFIX', 'IP-ASN', 'GEOIP'])

/**
 * `cont` says which targets end the matching (see ./continuation): Mihomo follows a group into its current choice and goes on
 * when that is PASS, so a match on such a group is not the last word.
 */
export function routeFor(doc: Doc, probe: Probe, cont: Continuation): RouteResult {
  const out: RouteResult = { first: null, final: true, certain: true, maybe: [], maybeTotal: 0, later: [], laterTotal: 0, passed: [] }
  const p: Probe = { ...probe, resolvedEarlier: false }
  for (let i = 0; i < doc.lines.length; i++) {
    const line = doc.lines[i]
    const rule = line.rule
    if (line.kind !== 'rule' || !rule) continue
    const upper = rule.type.toUpperCase()
    // A rule with no outbound sends nothing anywhere, unless it is a jump into a sub-rule.
    if (rule.target === '' && upper !== 'SUB-RULE') continue
    const v = evalRule(rule, p)
    // An address rule that does not say no-resolve makes Mihomo resolve a domain when it is reached, match or not.
    if (p.kind === 'domain' && RESOLVES.has(upper) && !hasOption(rule.options, 'no-resolve') && !hasOption(rule.options, 'src')) p.resolvedEarlier = true
    if (v.verdict === 'no') continue
    if (v.verdict === 'unknown') {
      if (out.first === null) {
        out.maybeTotal++
        if (out.maybe.length < CAP) out.maybe.push({ line: i, target: rule.target, reason: v.reason })
      }
      continue
    }
    const hit = { line: i, target: rule.target }
    if (out.first === null) {
      if (rule.target.toUpperCase() === 'PASS') { out.passed.push(hit); continue }
      out.first = hit
      out.final = !cont.mayContinue(rule.target)
    } else {
      out.laterTotal++
      if (out.later.length < CAP) out.later.push(hit)
    }
  }
  out.certain = out.maybeTotal === 0 && out.final
  return out
}
