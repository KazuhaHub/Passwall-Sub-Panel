import type { TFunction } from 'i18next'
import { spacedT } from '@/i18n/spacing'
import type { Tone } from '@/components/common/tones'
import type { LandingColor } from './landingColor'

export type PathNodeKind = 'user' | 'entry' | 'cdn' | 'external' | 'landing' | 'exit' | 'internet'
/** `pending` is a line that has not taken effect yet: its own tone, not a milder warning. */
export type PathTone = 'success' | 'warning' | 'danger' | 'pending' | 'neutral'

/** A pending line is drawn in the warning tone (it has not taken effect yet) and told apart by its own wording. */
export function toneOfPath(status: PathTone): Tone {
  return status === 'pending' ? 'warning' : status
}

export interface PathNode {
  id: string
  kind: PathNodeKind
  /** The node's own name (a server, a landing node). Left out for the generic nodes (the user, the internet), which the diagram package names itself in the UI language. */
  label?: string
  /** A port or a region: small text under (md) or after (sm) the label. */
  sublabel?: string
  status?: PathTone
  /** For a landing node: the colour that ties it to the same landing elsewhere on the page. */
  landingColor?: LandingColor
}

/** edges[i] joins nodes[i] and nodes[i + 1]. */
export interface PathEdge {
  /** solid = 端口转发 (traffic passes through unchanged); dashed = 加密隧道. */
  style: 'solid' | 'dashed'
  /** Short text on the edge: a latency. Part of the spoken summary too. */
  label?: string
  /**
   * Overrides the wording the style would give ("回源" for a CDN hop). An empty
   * string says the hop has no name worth speaking (a direct line, landing to
   * internet); its label, if any, is then attached to the node it leads to.
   */
  name?: string
  /** `origin`: a CDN fetching from the origin (回源), named in the UI language. */
  role?: 'origin'
  status?: PathTone
}

/** A node's name: its own label, or the generic name of its kind in the UI language. */
export function nodeLabel(node: PathNode, t: TFunction): string {
  return node.label ?? t(`ui.diagram.kind_${node.kind}`)
}

type Problem = 'danger' | 'warning' | 'pending'
const PROBLEMS: Problem[] = ['danger', 'warning', 'pending']

function problemOf(status: PathTone | undefined): Problem | null {
  return status === 'danger' || status === 'warning' || status === 'pending' ? status : null
}

/** The wording of a non-normal status; null for healthy and neutral, which are never spoken. */
export function statusWord(status: PathTone | undefined, t: TFunction): string | null {
  const p = problemOf(status)
  return p ? t(`ui.diagram.status_${p}`) : null
}

function bracket(parts: (string | null | undefined)[], t: TFunction, head: string): string {
  const real = parts.filter((p): p is string => !!p)
  if (!real.length) return head
  return t('ui.diagram.summary_node', { label: head, parts: real.join(t('ui.diagram.summary_sep')) })
}

/** A node's wording: its label, with a CDN or external entry named as such (the picture says it only by icon), its port, and any text an unnamed hop attached to it. */
function nodeText(node: PathNode, t: TFunction, extra: (string | undefined)[] = []): string {
  const kind = node.kind === 'cdn' ? t('ui.diagram.kind_cdn') : node.kind === 'external' ? t('ui.diagram.kind_external') : undefined
  return bracket([kind, node.sublabel, ...extra], t, nodeLabel(node, t))
}

/** An edge's wording: `phrase` goes in the sentence ("端口转发 (48 ms)"); `tail` is the label an unnamed edge hands to its destination. */
export function edgeText(edge: PathEdge | undefined, t: TFunction): { phrase: string; tail: string | undefined } {
  const e = edge ?? { style: 'solid' as const }
  const name = e.name ?? (e.role === 'origin' ? t('ui.diagram.edge_origin') : t(e.style === 'dashed' ? 'ui.diagram.edge_dashed' : 'ui.diagram.edge_solid'))
  if (!name) return { phrase: '', tail: e.label }
  return { phrase: e.label ? `${name}${t('ui.diagram.summary_paren', { parts: e.label })}` : name, tail: undefined }
}

/**
 * The status clause: every non-normal status said ONCE, with what it applies to.
 * "Whole line" when every element between the end nodes has it (the origin and
 * the internet are the same on every line, so they do not count); otherwise the
 * elements are listed, nodes by name and edges as "A 到 B 这一段".
 */
function statusClause(nodes: readonly PathNode[], edges: readonly (PathEdge | undefined)[], t: TFunction): string {
  const inner = (i: number) => nodes[i].kind !== 'user' && nodes[i].kind !== 'internet'
  const considered: { problem: Problem | null; name: string }[] = []
  const everything: { problem: Problem | null; name: string }[] = []
  nodes.forEach((n, i) => {
    const el = { problem: problemOf(n.status), name: nodeLabel(n, t) }
    everything.push(el)
    if (inner(i)) considered.push(el)
  })
  edges.forEach((e, i) => {
    if (i >= nodes.length - 1) return
    const el = { problem: problemOf(e?.status), name: t('ui.diagram.status_edge_ref', { from: nodeLabel(nodes[i], t), to: nodeLabel(nodes[i + 1], t) }) }
    everything.push(el)
    if (inner(i) && inner(i + 1)) considered.push(el)
  })
  const clauses: string[] = []
  for (const p of PROBLEMS) {
    const having = everything.filter(el => el.problem === p)
    if (!having.length) continue
    const whole = considered.length > 0 && considered.every(el => el.problem === p)
    const word = t(`ui.diagram.status_${p}`)
    clauses.push(whole
      ? t('ui.diagram.status_whole', { status: word })
      : t('ui.diagram.status_items', { items: having.map(el => el.name).join(t('ui.diagram.summary_item_sep')), status: word }))
  }
  return clauses.join(t('ui.diagram.status_sep'))
}

/**
 * One sentence for the whole path, so a diagram is never the only source of its
 * information (plan 3.6): "用户 经 Taiwan HiNet Dynamic 端口转发 到 Irvine Home".
 *
 * A relay line reads as origin, the server it connects through, and where that
 * server passes it on; a DIRECT line (the origin meets the landing node itself)
 * reads "用户 直连 Irvine Home" and is never confused with a relay. What follows the
 * landing node is told as what it is: an exit server is "再经出口服务器 X 出网", the
 * internet after it is not repeated, anything else continues with "，再经…到…".
 * A CDN or an external entry is named as such, because the picture shows it only
 * by its icon. An unnamed edge hands its label (a latency) to the node it leads to.
 *
 * STATUS is said once per kind, after the sentence ("；整条线路 等待生效" or the
 * elements that have it), and never inside the brackets, so a node and an edge that
 * share a status do not repeat it. Healthy and neutral are not spoken.
 *
 * Spaces between Chinese and Latin text at the joins are resolved at render time
 * (src/i18n/spacing.ts); nothing here rewrites a label.
 */
export function describePath(nodes: readonly PathNode[], edges: readonly (PathEdge | undefined)[], rawT: TFunction): string {
  // Every sentence here joins values to template text: the boundaries are resolved by the spacing rule.
  const t = spacedT(rawT)
  if (nodes.length === 0) return t('ui.diagram.summary_empty')
  if (nodes.length === 1) return nodeText(nodes[0], t)

  const li = nodes.findIndex(n => n.kind === 'landing')
  const direct = li === 1
  let out: string
  let next: number

  if (nodes.length === 2 || direct) {
    const e = edgeText(edges[0], t)
    const to = nodeText(nodes[1], t, [e.tail])
    out = e.phrase
      ? t('ui.diagram.summary_direct', { from: nodeText(nodes[0], t), to, edge: e.phrase })
      : t(direct ? 'ui.diagram.summary_direct_line' : 'ui.diagram.summary_plain', { from: nodeText(nodes[0], t), to })
    next = 2
  } else {
    const e = edgeText(edges[1], t)
    const from = nodeText(nodes[0], t)
    const via = nodeText(nodes[1], t)
    out = e.phrase
      ? t('ui.diagram.summary_via', { from, via, edge: e.phrase, to: nodeText(nodes[2], t) })
      : t('ui.diagram.summary_via_plain', { from, via, to: nodeText(nodes[2], t, [e.tail]) })
    next = 3
  }

  for (let i = next; i < nodes.length; i++) {
    const node = nodes[i]
    const e = edgeText(edges[i - 1], t)
    if (node.kind === 'exit') {
      // A hop that is a tunnel or has a name says so; a plain one is just "出网".
      out += edges[i - 1]?.style === 'dashed' || (e.phrase && edges[i - 1]?.name)
        ? t('ui.diagram.summary_exit_edge', { edge: e.phrase, exit: nodeText(node, t) })
        : t('ui.diagram.summary_exit', { exit: nodeText(node, t, [e.tail]) })
      // The internet after an exit server is what "出网" already said.
      if (nodes[i + 1]?.kind === 'internet') i++
      continue
    }
    out += e.phrase
      ? t('ui.diagram.summary_then', { edge: e.phrase, to: nodeText(node, t) })
      : t('ui.diagram.summary_then_plain', { to: nodeText(node, t, [e.tail]) })
  }

  const status = statusClause(nodes, edges, t)
  return status ? `${out}${t('ui.diagram.status_sep')}${status}` : out
}
