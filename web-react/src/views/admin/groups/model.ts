import type { createGroup, updateGroup } from '@/api/groups'
import type { Group, TagFilter } from '@/api/types'

/** What the group sheet edits. Everything the old dialog's form held, and nothing else. */
export interface GroupForm {
  slug: string
  name: string
  all: boolean
  /** AND vs OR over the conditions. */
  mode: 'all' | 'any'
  // Conditions are split by prefix at edit time so the UI can render three
  // dedicated pickers, then rejoined on save into the backend's
  // `tag_filter.tags []string` shape ("region:US", "tag:Premium", "node:1.1.1.1").
  // Anything that doesn't match a known prefix lives in `custom_text` so admins
  // running advanced conditions (e.g. "vendor:gcp" stored as a literal tag) can
  // still round-trip them.
  regions: string[]
  tags: string[]
  servers: string[]
  custom_text: string
  remark: string
  require_2fa: boolean
  /**
   * The group's entitlement policy. null = the group states nothing, which is NOT
   * the same as 0 (explicitly uncapped). A number box that can be empty keeps the
   * two apart; a text box turned into a number would not.
   */
  traffic_limit_gb: number | null
  ip_limit: number | null
  device_limit: number | null
}

export const EMPTY_FORM: GroupForm = {
  slug: '', name: '', all: false, mode: 'all',
  regions: [], tags: [], servers: [], custom_text: '',
  remark: '',
  require_2fa: false,
  traffic_limit_gb: null, ip_limit: null, device_limit: null,
}

/**
 * parseTagConditions splits a stored tag_filter.tags array into the four
 * buckets the UI exposes. The backend's matchOne dispatch handles
 * region:/tag:/node: prefixes specially, anything else (including bare
 * strings) is treated as a literal tag lookup — see internal/service/group.
 *
 * `server:` is the legacy spelling of `node:` (renamed when the UI label moved
 * from "Server" to "Node"). Existing stored conditions read into the same
 * bucket; on save they're rewritten to the new prefix so the legacy form
 * drains out naturally.
 */
export function parseTagConditions(conds: string[]): { regions: string[]; tags: string[]; servers: string[]; custom: string[] } {
  const out = { regions: [] as string[], tags: [] as string[], servers: [] as string[], custom: [] as string[] }
  for (const c of conds) {
    const s = c.trim()
    if (!s) continue
    const i = s.indexOf(':')
    if (i > 0) {
      const key = s.slice(0, i).trim()
      const val = s.slice(i + 1).trim()
      if (key === 'region') { out.regions.push(val); continue }
      if (key === 'tag') { out.tags.push(val); continue }
      if (key === 'node' || key === 'server') { out.servers.push(val); continue }
    }
    out.custom.push(s)
  }
  return out
}

/** buildTagConditions packages the buckets back into the backend's flat conditions array. Empty entries are dropped. */
export function buildTagConditions(f: GroupForm): string[] {
  const conds: string[] = []
  for (const r of f.regions) { const v = r.trim(); if (v) conds.push(`region:${v}`) }
  for (const tg of f.tags) { const v = tg.trim(); if (v) conds.push(`tag:${v}`) }
  for (const sv of f.servers) { const v = sv.trim(); if (v) conds.push(`node:${v}`) }
  for (const raw of f.custom_text.split(',')) {
    const v = raw.trim()
    if (v) conds.push(v)
  }
  return conds
}

/** The tag_filter exactly as a save sends it, and as the live preview asks about it. */
export function tagFilterOf(f: GroupForm): TagFilter {
  return {
    all: f.all,
    // Send "all" / "any" — backend treats empty as "all", but being explicit
    // makes the wire shape self-describing.
    mode: f.mode,
    tags: f.all ? [] : buildTagConditions(f),
  }
}

export function formOf(g: Group): GroupForm {
  const parsed = parseTagConditions(g.tag_filter.tags || [])
  return {
    slug: g.slug,
    name: g.name,
    all: g.tag_filter.all,
    mode: g.tag_filter.mode === 'any' ? 'any' : 'all',
    regions: parsed.regions,
    tags: parsed.tags,
    servers: parsed.servers,
    custom_text: parsed.custom.join(', '),
    remark: g.remark || '',
    require_2fa: !!g.require_2fa,
    traffic_limit_gb: g.traffic_limit_gb ?? null,
    ip_limit: g.ip_limit ?? null,
    device_limit: g.device_limit ?? null,
  }
}

export const sameForm = (a: GroupForm, b: GroupForm) => JSON.stringify(a) === JSON.stringify(b)

type LimitKey = 'traffic_limit_gb' | 'ip_limit' | 'device_limit'
type ClearKey = 'clear_traffic_limit' | 'clear_ip_limit' | 'clear_device_limit'

/**
 * limitField turns one form value into the sparse-update shape.
 *
 * An empty value means "this group states nothing", and a sparse DTO cannot
 * express that with a value — omitting the key means "leave it alone", which is
 * the opposite. So an empty field sends the clear flag instead. A typed 0 is a
 * real value ("uncapped") and is sent as one; conflating the two is the same
 * mistake the whole third state exists to prevent.
 */
function limitField(key: LimitKey, value: number | null, clearKey: ClearKey): Record<string, number | boolean> {
  if (value === null) return { [clearKey]: true }
  return { [key]: value }
}

export type UpdateGroupRequest = Parameters<typeof updateGroup>[1]
export type CreateGroupRequest = Parameters<typeof createGroup>[0]

/** The body of PUT /admin/groups/:id. */
export function updateRequest(f: GroupForm): UpdateGroupRequest {
  return {
    name: f.name,
    tag_filter: tagFilterOf(f),
    remark: f.remark,
    require_2fa: f.require_2fa,
    // An emptied field must CLEAR the policy, and a sparse request cannot say
    // that with a value — hence the explicit flags.
    ...limitField('traffic_limit_gb', f.traffic_limit_gb, 'clear_traffic_limit'),
    ...limitField('ip_limit', f.ip_limit, 'clear_ip_limit'),
    ...limitField('device_limit', f.device_limit, 'clear_device_limit'),
  }
}

/** The body of POST /admin/groups. */
export function createRequest(f: GroupForm): CreateGroupRequest {
  return {
    slug: f.slug,
    name: f.name,
    tag_filter: tagFilterOf(f),
    remark: f.remark,
    require_2fa: f.require_2fa,
    // On create an empty field is simply omitted, which is already "states nothing" — no clear flag needed.
    ...(f.traffic_limit_gb === null ? {} : { traffic_limit_gb: f.traffic_limit_gb }),
    ...(f.ip_limit === null ? {} : { ip_limit: f.ip_limit }),
    ...(f.device_limit === null ? {} : { device_limit: f.device_limit }),
  }
}

/** The matcher's number of nodes and lines a saved group's filter reaches, as the list shows it. */
export interface MatchCounts { nodes: number; visibleLines: number; totalLines: number }
