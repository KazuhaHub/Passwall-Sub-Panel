import type { RuleSet } from '@/api/rules'
import { buildBlocks } from './blocks'
import { renameTarget } from './ops'
import { parseDoc, serializeDoc, type Doc } from './parse'

/**
 * Renaming a proxy group (design 3.11). A group has no record of its own: it exists because rules name it, so a rename is
 * a rewrite of every rule line that names it plus the settings keyed by the name (order, member lists, options) and the
 * member lists of other groups that point at it. Done in the browser it is one edit of the draft; done by hand it ends
 * with the old group's settings deleted on save and a new group made from default members.
 *
 * What it will not do is as important as what it does. A line the editor reads as text (a quoted rule, a `{{` line) is
 * never rewritten, and if one of those still names the group the rename would leave a rule pointing at the OLD name, which
 * the backend turns into a brand-new group. So such a rename is refused, with the reason, instead of done half way.
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

export type RenamePlan =
  | { ok: true; patch: Patch; rules: number; refs: number }
  | { ok: false; reason: 'same' | 'text_only' | 'unreadable' }

/** A line the editor reads as text that names the group somewhere in it. */
const mentionsInText = (doc: Doc, name: string) => doc.lines.some(l => l.kind === 'text' && l.raw.includes(name))

export function planRename(rs: RuleSet, from: string, to: string): RenamePlan {
  if (from === to) return { ok: false, reason: 'same' }
  const main = parseDoc(rs.content)
  if (main.foreign.length > 0) return { ok: false, reason: 'text_only' }
  if (mentionsInText(main, from)) return { ok: false, reason: 'unreadable' }

  const subs = rs.mihomo_sub_rules ?? []
  const subDocs = subs.map(s => parseDoc(s.content))
  for (const d of subDocs) {
    // A sub-rule the editor cannot read (a foreign line anywhere in it) is left byte for byte as it is, so it must not name the
    // group at all: a rule left pointing at the old name would become a new group. A text line naming it is the same problem.
    const textual = d.foreign.length > 0 || d.lines.some(l => l.kind === 'text')
    if (textual && d.lines.some(l => (d.foreign.length > 0 || l.kind === 'text') && l.raw.includes(from))) return { ok: false, reason: 'unreadable' }
  }

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

  const order = rs.proxy_group_order ?? []
  if (order.includes(from)) patch.proxy_group_order = order.map(n => (n === from ? to : n))

  let refs = 0
  const members = rs.proxy_group_members
  if (members) {
    const hasKey = Object.prototype.hasOwnProperty.call(members, from)
    const points = Object.values(members).some(list => list.some(m => m.kind === 'proxy_group' && m.value === from))
    if (hasKey || points) {
      const next: NonNullable<RuleSet['proxy_group_members']> = {}
      for (const [key, list] of Object.entries(members)) {
        next[key === from ? to : key] = list.map(m => {
          if (m.kind === 'proxy_group' && m.value === from) { refs++; return { ...m, value: to } }
          return m
        })
      }
      patch.proxy_group_members = next
    }
  }

  const options = rs.proxy_group_options
  if (options && Object.prototype.hasOwnProperty.call(options, from)) {
    const next: NonNullable<RuleSet['proxy_group_options']> = {}
    for (const [key, value] of Object.entries(options)) next[key === from ? to : key] = value
    patch.proxy_group_options = next
  }

  return { ok: true, patch, rules, refs }
}
