import type { ProxyGroupMember, RuleSet } from '@/api/rules'
import { buildBlocks } from './blocks'
import { renameTarget } from './ops'
import { parseDoc, serializeDoc, splitFields, type Doc } from './parse'
import { normalizeTarget } from './target'

/**
 * Renaming a proxy group (design 3.11). A group has no record of its own: it exists because rules name it, so a rename is
 * a rewrite of every rule line that names it plus the settings keyed by the name (order, member lists, options) and the
 * member lists of other groups that point at it.
 *
 * The rename must leave the EFFECTIVE configuration as it was, the name apart. The backend derives a group's members from its
 * NAME when no list is stored (proxyGroupChoices in internal/service/render/proxy_groups.go: `广告拦截` blocks, `节点选择`
 * is the node pool, anything unknown gets the catch-all), so a bare rename would hand the new name the catch-all members
 * (ads stop being blocked) and leave every default list that named the old group pointing at a group the backend then makes
 * again. So the lists that depend on the name are written out explicitly, taken from the backend's own inspection of the
 * draft (`effective`: its `members` per group), not worked out here. Without that data the rename is not offered.
 *
 * What it will not do is as important. A line the editor reads as text (a quoted rule, a `{{` line) is never rewritten, and if
 * one of those still names the group the rename would leave a rule pointing at the OLD name, which the backend turns into a
 * brand-new group. So such a rename is refused, with the reason, instead of done half way.
 */

/** How many runs of rules (groups in the Rules tab) and rule lines name `name`, across the main rules and the sub-rules. */
export function groupUsage(rs: Pick<RuleSet, 'content' | 'mihomo_sub_rules'>, name: string): { blocks: number; rules: number } {
  let blocks = 0
  let rules = 0
  const count = (doc: Doc) => {
    for (const b of buildBlocks(doc)) if (b.kind === 'rules' && b.target === name) { blocks++; rules += b.count }
  }
  count(parseDoc(rs.content))
  for (const sub of rs.mihomo_sub_rules ?? []) count(parseDoc(sub.content))
  return { blocks, rules }
}

type Patch = Partial<Pick<RuleSet, 'content' | 'mihomo_sub_rules' | 'proxy_group_order' | 'proxy_group_members' | 'proxy_group_options'>>

export type RenameRefusal = 'same' | 'text_only' | 'unreadable' | 'taken' | 'unused' | 'no_data'
export type RenamePlan =
  | { ok: true; patch: Patch; rules: number; refs: number; /** Member lists written out explicitly that followed the name before. */ materialised: number }
  | { ok: false; reason: RenameRefusal }

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/**
 * Whether a line the editor reads only as text names `name` as a part of the line. Where the line can be split into rule fields
 * (once its YAML item marker and whole-scalar quotes are off) the fields are compared the way the backend reads them; where it
 * cannot, the name must appear as a whole word, so `ProxyAll` is not `Proxy`. When in doubt it says yes: the cost of a false
 * yes is a rename that has to be done in the Text view, the cost of a false no is a rule left pointing at a group that is gone.
 */
export function mentionsName(raw: string, name: string): boolean {
  let body = raw.replace(/\r$/, '').trim().replace(/^-\s+/, '')
  if (body.length >= 2 && (body[0] === "'" || body[0] === '"') && body[body.length - 1] === body[0]) body = body.slice(1, -1)
  const fields = splitFields(body)
  if (fields.length >= 2) return fields.some(f => normalizeTarget(f) === name)
  return new RegExp(`(^|[^\\p{L}\\p{N}_])${escapeRe(name)}($|[^\\p{L}\\p{N}_])`, 'u').test(raw)
}

const swap = (list: readonly ProxyGroupMember[], from: string, to: string): { list: ProxyGroupMember[]; hits: number } => {
  let hits = 0
  const out = list.map(m => {
    if (m.kind === 'proxy_group' && m.value === from) { hits++; return { ...m, value: to } }
    return m
  })
  return { list: out, hits }
}

export function planRename(
  rs: RuleSet, from: string, to: string, effective: ReadonlyMap<string, readonly ProxyGroupMember[]>,
  /** Node and subscription-line names the inspection reports: the renderer does not make a group of a name that is one of those (proxyTargetExclusions). */
  reserved: ReadonlySet<string> = new Set(),
): RenamePlan {
  if (from === to) return { ok: false, reason: 'same' }
  const main = parseDoc(rs.content)
  if (main.foreign.length > 0) return { ok: false, reason: 'text_only' }
  if (main.lines.some(l => l.kind === 'text' && mentionsName(l.raw, from))) return { ok: false, reason: 'unreadable' }

  const subs = rs.mihomo_sub_rules ?? []
  const subDocs = subs.map(s => parseDoc(s.content))
  for (const d of subDocs) {
    // A sub-rule the editor cannot read (a foreign line anywhere in it) is left byte for byte as it is, so it must not name the
    // group at all: a rule left pointing at the old name would become a new group. A text line naming it is the same problem.
    const unreadable = d.foreign.length > 0
    if (d.lines.some(l => (unreadable || l.kind === 'text') && mentionsName(l.raw, from))) return { ok: false, reason: 'unreadable' }
  }

  // A setting left over under the new name (a group that no longer exists) would be adopted by the renamed group, whichever
  // key came first, so the name is not free.
  const explicit = rs.proxy_group_members ?? {}
  const options = rs.proxy_group_options
  if (Object.prototype.hasOwnProperty.call(explicit, to) || (options && Object.prototype.hasOwnProperty.call(options, to)) || effective.has(to) || reserved.has(to)) return { ok: false, reason: 'taken' }

  const patch: Patch = {}
  let rules = 0
  const renamed = renameTarget(main, from, to)
  rules += renamed.changed
  if (renamed.changed > 0) patch.content = serializeDoc(renamed.doc)
  if (subs.length > 0) {
    let touched = false
    const next = subs.map((s, i) => {
      if (subDocs[i].foreign.length > 0) return s
      const r = renameTarget(subDocs[i], from, to)
      if (r.changed === 0) return s
      touched = true
      rules += r.changed
      return { ...s, content: serializeDoc(r.doc) }
    })
    if (touched) patch.mihomo_sub_rules = next
  }
  // A group nothing but a member list names is not made by any rule, so there is nothing to rename it to.
  if (rules === 0) return { ok: false, reason: 'unused' }
  if (!effective.has(from)) return { ok: false, reason: 'no_data' }

  const order = rs.proxy_group_order ?? []
  if (order.includes(from)) patch.proxy_group_order = order.map(n => (n === from ? to : n))

  let refs = 0
  let materialised = 0
  const members: NonNullable<RuleSet['proxy_group_members']> = {}
  // The lists that are stored: moved to the new name if they were the group's own, repointed if they named it.
  for (const [key, list] of Object.entries(explicit)) {
    const r = swap(list, from, to)
    refs += r.hits
    members[key === from ? to : key] = r.list
  }
  // The lists that were not stored: the backend derived them (from the name, and with the old group's name in them where
  // it is a default member). Write the derived list out, with the new name, so the configuration stays what it was.
  for (const [name, list] of effective) {
    if (Object.prototype.hasOwnProperty.call(explicit, name)) continue
    const r = swap(list, from, to)
    if (name !== from && r.hits === 0) continue
    members[name === from ? to : name] = r.list
    materialised++
  }
  if (Object.keys(members).length > 0 || Object.keys(explicit).length > 0) patch.proxy_group_members = members

  if (options && Object.prototype.hasOwnProperty.call(options, from)) {
    const next: NonNullable<RuleSet['proxy_group_options']> = {}
    for (const [key, value] of Object.entries(options)) next[key === from ? to : key] = value
    patch.proxy_group_options = next
  }

  return { ok: true, patch, rules, refs, materialised }
}
