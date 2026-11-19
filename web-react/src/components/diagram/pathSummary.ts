import type { TFunction } from 'i18next'

export type PathNodeKind = 'user' | 'entry' | 'cdn' | 'external' | 'landing' | 'exit' | 'internet'
export type PathTone = 'success' | 'warning' | 'danger' | 'neutral'

export interface PathNode {
  id: string
  kind: PathNodeKind
  label: string
  /** A port or a region: small text under (md) or after (sm) the label. */
  sublabel?: string
  status?: PathTone
  /** Landing node id, for the colour bar that ties the same landing together across a page. */
  colorId?: string | number
}

/** edges[i] joins nodes[i] and nodes[i + 1]. */
export interface PathEdge {
  /** solid = 端口转发 (traffic passes through unchanged); dashed = 加密隧道. */
  style: 'solid' | 'dashed'
  /** Short text on the edge: a latency, "等待生效". Part of the spoken summary too. */
  label?: string
  /**
   * Overrides the wording the style would give ("回源" for a CDN hop). An empty
   * string says the hop has no name worth speaking (landing to internet).
   */
  name?: string
  status?: PathTone
}

/** The wording for one edge: its name (or the style's), with its label in brackets. '' when it has neither. */
export function edgeText(edge: PathEdge | undefined, t: TFunction): string {
  const e = edge ?? { style: 'solid' as const }
  const name = e.name ?? t(e.style === 'dashed' ? 'ui.diagram.edge_dashed' : 'ui.diagram.edge_solid')
  if (!name) return e.label ?? ''
  return e.label ? t('ui.diagram.summary_edge', { name, label: e.label }) : name
}

function nodeText(node: PathNode, t: TFunction): string {
  return node.sublabel ? t('ui.diagram.summary_node', { label: node.label, sub: node.sublabel }) : node.label
}

/**
 * One sentence for the whole path, so a diagram is never the only source of its
 * information (plan 3.6): "用户 经 Taiwan HiNet Dynamic 端口转发 到 Irvine Home".
 * A path reads as origin, the server the origin connects through, and where that
 * server passes it on; each further node continues with ", then ...". An edge
 * with no wording ('' name and no label) drops its phrase rather than speak an
 * empty one. Edge 0 (the origin's own hop) is not named in the sentence; every
 * edge still carries its own `title` in the picture.
 */
export function describePath(nodes: readonly PathNode[], edges: readonly (PathEdge | undefined)[], t: TFunction): string {
  if (nodes.length === 0) return t('ui.diagram.summary_empty')
  if (nodes.length === 1) return nodeText(nodes[0], t)
  const n = nodes.map(x => nodeText(x, t))

  if (nodes.length === 2) {
    const edge = edgeText(edges[0], t)
    return edge
      ? t('ui.diagram.summary_direct', { from: n[0], to: n[1], edge })
      : t('ui.diagram.summary_plain', { from: n[0], to: n[1] })
  }

  const first = edgeText(edges[1], t)
  let out = first
    ? t('ui.diagram.summary_via', { from: n[0], via: n[1], edge: first, to: n[2] })
    : t('ui.diagram.summary_via_plain', { from: n[0], via: n[1], to: n[2] })
  for (let i = 3; i < nodes.length; i++) {
    const edge = edgeText(edges[i - 1], t)
    out += edge
      ? t('ui.diagram.summary_then', { edge, to: n[i] })
      : t('ui.diagram.summary_then_plain', { to: n[i] })
  }
  return out
}
