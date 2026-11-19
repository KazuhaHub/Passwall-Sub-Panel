import type { TFunction } from 'i18next'
import { autoSpace } from '@/lib/cjkSpacing'
import type { LandingColor } from './landingColor'

export type PathNodeKind = 'user' | 'entry' | 'cdn' | 'external' | 'landing' | 'exit' | 'internet'
export type PathTone = 'success' | 'warning' | 'danger' | 'neutral'

export interface PathNode {
  id: string
  kind: PathNodeKind
  label: string
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
  /** Short text on the edge: a latency, "等待生效". Part of the spoken summary too. */
  label?: string
  /**
   * Overrides the wording the style would give ("回源" for a CDN hop). An empty
   * string says the hop has no name worth speaking (landing to internet); its
   * label, if any, is then attached to the node it leads to.
   */
  name?: string
  status?: PathTone
}

// Only a problem is spoken: healthy and neutral are the default and need no words.
function statusWord(status: PathTone | undefined, t: TFunction): string | null {
  if (status === 'danger') return t('ui.diagram.status_danger')
  if (status === 'warning') return t('ui.diagram.status_warning')
  return null
}

function bracket(parts: (string | null | undefined)[], t: TFunction, key: 'ui.diagram.summary_paren' | 'ui.diagram.summary_node', head?: string): string {
  const real = parts.filter((p): p is string => !!p)
  if (!real.length) return head ?? ''
  const joined = real.join(t('ui.diagram.summary_sep'))
  return key === 'ui.diagram.summary_node' ? t(key, { label: head, parts: joined }) : t(key, { parts: joined })
}

function nodeText(node: PathNode, t: TFunction): string {
  return bracket([node.sublabel, statusWord(node.status, t)], t, 'ui.diagram.summary_node', node.label)
}

/** An edge's wording: `phrase` goes in the sentence ("端口转发（48 ms）"); `tail` is what an unnamed edge attaches to its destination. */
export function edgeText(edge: PathEdge | undefined, t: TFunction): { phrase: string; tail: string } {
  const e = edge ?? { style: 'solid' as const }
  const name = e.name ?? t(e.style === 'dashed' ? 'ui.diagram.edge_dashed' : 'ui.diagram.edge_solid')
  const paren = bracket([e.label, statusWord(e.status, t)], t, 'ui.diagram.summary_paren')
  return name ? { phrase: `${name}${paren}`, tail: '' } : { phrase: '', tail: paren }
}

/**
 * One sentence for the whole path, so a diagram is never the only source of its
 * information (plan 3.6): "用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home".
 * A path reads as origin, the server the origin connects through, and where that
 * server passes it on; each further node continues with "，再经…到…".
 *
 * Whatever is NOT normal is said: a node or an edge that is down or needs
 * attention carries its status in brackets, and an unnamed edge attaches its label
 * (a latency) to the node it leads to rather than reading as a hop. Edge 0 (the
 * origin's own hop) is not named in the sentence, so a problem on it is added as a
 * clause of its own. Every edge also has a `title` in the picture.
 *
 * Chinese templates carry no spaces at the joins; autoSpace adds the ones copy
 * rule 8 asks for between Chinese and Latin text.
 */
export function describePath(nodes: readonly PathNode[], edges: readonly (PathEdge | undefined)[], t: TFunction): string {
  if (nodes.length === 0) return t('ui.diagram.summary_empty')
  if (nodes.length === 1) return autoSpace(nodeText(nodes[0], t))
  const n = nodes.map(x => nodeText(x, t))

  if (nodes.length === 2) {
    const edge = edgeText(edges[0], t)
    return autoSpace(edge.phrase
      ? t('ui.diagram.summary_direct', { from: n[0], to: n[1], edge: edge.phrase })
      : t('ui.diagram.summary_plain', { from: n[0], to: `${n[1]}${edge.tail}` }))
  }

  const first = edgeText(edges[1], t)
  let out = first.phrase
    ? t('ui.diagram.summary_via', { from: n[0], via: n[1], edge: first.phrase, to: n[2] })
    : t('ui.diagram.summary_via_plain', { from: n[0], via: n[1], to: `${n[2]}${first.tail}` })
  for (let i = 3; i < nodes.length; i++) {
    const edge = edgeText(edges[i - 1], t)
    out += edge.phrase
      ? t('ui.diagram.summary_then', { edge: edge.phrase, to: n[i] })
      : t('ui.diagram.summary_then_plain', { to: `${n[i]}${edge.tail}` })
  }
  const head = edges[0]
  const word = statusWord(head?.status, t)
  if (word) out += t('ui.diagram.summary_issue_edge', { from: n[0], to: n[1], status: word })
  return autoSpace(out)
}
