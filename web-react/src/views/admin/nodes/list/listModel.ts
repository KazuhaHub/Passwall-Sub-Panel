import type { OrderRow } from '@/api/nodes'
import type { Node } from '@/api/types'
import { filterRows } from '@/components/data/dataTableLogic'
import { assignLandingColors, type LandingColor } from '@/components/diagram/landingColor'
import { UNREACHABLE_HEALTH } from '../shared/statusOf'
import { rowId } from './orderMoves'

// Pure model of the nodes list: what a search or facet keeps, what the KPI tiles count, the landing colours,
// which rows light up together, and the by-node tree. Kept free of React so every rule has a plain test.

export interface ListFilters {
  q: string
  type: readonly string[]
  status: readonly string[]
  entry: readonly string[]
  landing: readonly string[]
}

export const NO_FILTERS: ListFilters = { q: '', type: [], status: [], entry: [], landing: [] }

export const isFiltered = (f: ListFilters) => f.q.trim() !== '' || f.type.length > 0 || f.status.length > 0 || f.entry.length > 0 || f.landing.length > 0
const facetsActive = (f: ListFilters) => f.type.length > 0 || f.status.length > 0 || f.entry.length > 0 || f.landing.length > 0

export const isLine = (row: OrderRow) => row.kind === 'line'

/** Everything the search matches in a line row: names, servers, address, port, region, tags and the node id (`12` or `#12`). */
export function searchText(row: OrderRow, node?: Pick<Node, 'tags' | 'region'>): string {
  if (row.kind === 'separator') return row.name
  return [
    row.name, row.landing?.name, row.landing?.panel_name, row.entry?.panel_name, row.address, row.port || '',
    row.landing?.region, node?.region, ...(node?.tags ?? []), `${row.node_id}`, `#${row.node_id}`,
  ].filter(v => v !== undefined && v !== '').join(' ')
}

/** The Status facet's option values; a row matches an option, and a row matches the facet when it matches any selected one. */
export type StatusFacet = 'published' | 'unpublished' | 'unreachable' | 'entry_missing' | string

function matchesStatus(row: OrderRow, value: StatusFacet): boolean {
  switch (value) {
    case 'published': return row.published === true
    case 'unpublished': return row.published === false
    case 'unreachable': return (UNREACHABLE_HEALTH as readonly string[]).includes(row.health?.state ?? '')
    case 'entry_missing': return row.entry?.missing === true
    // Anything else is a withheld reason of its own.
    default: return row.withheld === value
  }
}

export function filterOrder(rows: readonly OrderRow[], nodes: ReadonlyMap<number, Node>, f: ListFilters): OrderRow[] {
  const keyword = f.q.trim()
  const facets = facetsActive(f)
  const lines = rows.filter(row => {
    // A separator has no facets to match, so any facet hides it; a keyword is then matched against its name.
    if (row.kind === 'separator') return !facets
    if (f.type.length && !f.type.includes(row.line_type ?? '')) return false
    if (f.status.length && !f.status.some(v => matchesStatus(row, v))) return false
    if (f.entry.length && !f.entry.includes(String(row.entry?.panel_id ?? ''))) return false
    if (f.landing.length && !f.landing.includes(String(row.landing?.panel_id ?? ''))) return false
    return true
  })
  // Separators stay only while a keyword matches their own name (they have no other fields to match).
  const matched = filterRows(lines, r => searchText(r, nodes.get(r.node_id ?? 0)), keyword)
  return matched
}

export interface Kpis { lines: number; published: number; unpublished: number; unreachable: number }

/** Separators are not lines and are not counted. */
export function kpisOf(rows: readonly OrderRow[]): Kpis {
  const lines = rows.filter(isLine)
  return {
    lines: lines.length,
    published: lines.filter(r => matchesStatus(r, 'published')).length,
    unpublished: lines.filter(r => matchesStatus(r, 'unpublished')).length,
    unreachable: lines.filter(r => matchesStatus(r, 'unreachable')).length,
  }
}

export interface FacetOptionData { value: string; label: string; count: number }

/** Options of the type, entry and landing facets with how many lines each would keep (over the unfiltered list). */
export function facetOptions(rows: readonly OrderRow[]) {
  const count = (pick: (r: OrderRow) => { value: string; label: string } | null) => {
    const m = new Map<string, FacetOptionData>()
    for (const r of rows.filter(isLine)) {
      const o = pick(r)
      if (!o) continue
      const cur = m.get(o.value)
      if (cur) cur.count++
      else m.set(o.value, { ...o, count: 1 })
    }
    return [...m.values()]
  }
  return {
    type: count(r => (r.line_type ? { value: r.line_type, label: r.line_type } : null)),
    entry: count(r => (r.entry ? { value: String(r.entry.panel_id), label: r.entry.panel_name } : null)),
    landing: count(r => (r.landing ? { value: String(r.landing.panel_id), label: r.landing.panel_name } : null)),
    statusCount: (value: StatusFacet) => rows.filter(isLine).filter(r => matchesStatus(r, value)).length,
  }
}

/** Colours by first appearance over the COMPLETE list's landing ids, sorted: a filter or a drag must not recolour rows. */
export function landingColorsOf(rows: readonly OrderRow[]): Map<string, LandingColor> {
  const ids = [...new Set(rows.filter(isLine).map(r => r.node_id as number))].sort((a, b) => a - b)
  return assignLandingColors(ids)
}

/** Rows that light up with the hovered one: every row of the same landing, and every row through the same entry server. */
export function relatedIds(rows: readonly OrderRow[], hoveredId: string | null): string[] {
  if (!hoveredId) return []
  const hovered = rows.find(r => rowId(r) === hoveredId)
  if (!hovered || hovered.kind !== 'line') return []
  return rows.filter(r => r.kind === 'line' && rowId(r) !== hoveredId
    && (r.node_id === hovered.node_id || (hovered.entry && r.entry?.panel_id === hovered.entry.panel_id))).map(rowId)
}

export interface TreeRow {
  id: string
  node_id: number
  name: string
  /** The direct row (absent when a filter removed it: the group still shows the landing). */
  direct?: OrderRow
  children: OrderRow[]
}

/** The by-node view: landings in the order of their direct row in the FULL list, each with its relay lines; separators are left out. */
export function buildTree(shown: readonly OrderRow[], full: readonly OrderRow[]): TreeRow[] {
  const directIndex = new Map<number, number>()
  full.forEach((r, i) => { if (r.kind === 'line' && r.line_id === 'direct') directIndex.set(r.node_id as number, i) })
  const groups = new Map<number, TreeRow>()
  for (const r of shown) {
    if (r.kind !== 'line') continue
    const id = r.node_id as number
    let g = groups.get(id)
    if (!g) { g = { id: `landing:${id}`, node_id: id, name: r.landing?.name ?? r.name, children: [] }; groups.set(id, g) }
    if (r.line_id === 'direct') g.direct = r
    else g.children.push(r)
  }
  return [...groups.values()].sort((a, b) => (directIndex.get(a.node_id) ?? Infinity) - (directIndex.get(b.node_id) ?? Infinity))
}
