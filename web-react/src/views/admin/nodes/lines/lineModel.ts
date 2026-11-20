import { lineLabelsInput } from '@/api/lineLabels'
import { parseSourceCIDRs } from '@/api/sourceGuard'
import type { RelayLine } from '@/api/types'
import { factorTextToMilli } from '@/utils/trafficFactor'

/** The port a CDN line is made with when none is given. */
export const CDN_DEFAULT_PORT = 443

/** Ports Cloudflare proxies for plain HTTP; a line on one defaults to no TLS to the CDN. */
const CDN_HTTP_PORTS = [80, 8080, 8880, 2052, 2082, 2086, 2095]
export const defaultClientTLS = (port: number) => !CDN_HTTP_PORTS.includes(port)

/**
 * The four kinds of line a node can offer besides its direct one (design 4.4.1). Derived from the line itself
 * rather than stored: a CDN line carries `mode: 'cdn'`, a tunnel line `mode: 'tunnel'` with an entry server, a
 * forward has only the entry server, an external entry has neither.
 */
export type LineKind = 'forward' | 'tunnel' | 'cdn' | 'external'

/**
 * A line as the editor holds it: the wire shape plus what is only about the form. `_source_text` is the source
 * addresses as typed (undefined = untouched, so the saved list is kept); `_factor_text` is the multiplier as
 * typed and `_factor_touched` says it was edited, because an untouched factor is not sent: a full replace that
 * echoed it would undo a concurrent change to it.
 */
export type LineDraft = RelayLine & {
  _source_text?: string
  _factor_text?: string
  _factor_touched?: boolean
}

export const kindOf = (d: Pick<RelayLine, 'mode' | 'entry_panel_id'>): LineKind =>
  d.mode === 'cdn' ? 'cdn' : (d.entry_panel_id ?? 0) > 0 ? (d.mode === 'tunnel' ? 'tunnel' : 'forward') : 'external'

/** A saved line as a draft; the server's own state on it (probe, running, listener) is read from the line, not the draft. */
export function draftOf(line: RelayLine): LineDraft {
  return { ...line, only_line_name: !!line.only_line_name }
}

/**
 * The start of a new line of one kind. Region and tags are copied from the node, as a line made in the old editor
 * did; the entry server, name and address come from the person's choice of server.
 */
export function blankDraft(kind: LineKind, node: { region: string; tags?: string[] }): LineDraft {
  const base: LineDraft = { name: '', only_line_name: false, address: '', port: 0, sni: '', host: '', enabled: true, region: node.region.trim(), tags: [...(node.tags ?? [])], entry_panel_id: 0 }
  switch (kind) {
    case 'cdn': return { ...base, mode: 'cdn', domain: '', port: CDN_DEFAULT_PORT }
    case 'tunnel': return { ...base, mode: 'tunnel' }
    default: return base
  }
}

/**
 * The line as the server's full-replace wants it. What the server decides (status, probe, the listener of a
 * tunnel line, CDN issues) is not sent back. Source addresses go only when they were typed, and never for a
 * tunnel line, which has none; the multiplier only when it was edited.
 */
export function wireLine(d: LineDraft): RelayLine {
  const { managed_ready: _r, probe: _p, probe_withheld: _pw, running: _run, tunnel: _t, cdn_issues: _i, source_withheld: _sw,
    _source_text, _factor_text: _ft, _factor_touched, traffic_factor_milli, ...r } = d
  return {
    ...r,
    ...(r.mode === 'tunnel' ? { source_cidrs: undefined } : _source_text !== undefined ? { source_cidrs: parseSourceCIDRs(_source_text) } : {}),
    ...(_factor_touched && traffic_factor_milli !== undefined ? { traffic_factor_milli } : {}),
    name: r.name.trim(),
    only_line_name: !!r.only_line_name,
    address: r.address.trim(),
    domain: r.mode === 'cdn' ? r.domain?.trim() : undefined,
    port: Number(r.port) || 0,
    sni: r.sni?.trim() || undefined,
    host: r.host?.trim() || undefined,
    entry_panel_id: r.entry_panel_id || undefined,
    ...lineLabelsInput(r),
  }
}

/** What the form changed, for telling an edit from a refresh: everything the editor edits, nothing the server decides. */
export function draftKey(d: LineDraft): string {
  const w = wireLine(d)
  // A tunnel line's port, SNI and Host follow its listener.
  return JSON.stringify(w.mode === 'tunnel' ? { ...w, port: 0, sni: undefined, host: undefined } : w)
}

export type LineField = 'name' | 'entry' | 'address' | 'port' | 'domain' | 'factor' | 'region'
export type LineProblems = Partial<Record<LineField, string>>

/** The messages' keys (under `admin:nodes.`): what is wrong and, where it can be said, how to fix it. */
export function validateDraft(d: LineDraft): LineProblems {
  const out: LineProblems = {}
  const kind = kindOf(d)
  if (d.name.trim().length > 64) out.name = 'line.err.name_long'
  if ((kind === 'forward' || kind === 'tunnel') && !(d.entry_panel_id && d.entry_panel_id > 0)) out.entry = 'line.err.entry_required'
  if (kind === 'cdn') { if (!d.domain?.trim()) out.domain = 'cdn.domain_required' }
  else if (!d.address.trim()) out.address = 'line.err.address_required'
  if (kind === 'forward' && !(Number(d.port) > 0)) out.port = 'line.err.port_required'
  if (Number(d.port) > 65535 || Number(d.port) < 0) out.port = 'line.err.port_range'
  if (d._factor_text !== undefined && factorTextToMilli(d._factor_text) === null) out.factor = 'validate.traffic_factor'
  if ((d.region ?? '').trim().length > 64) out.region = 'line.err.region_long'
  return out
}
