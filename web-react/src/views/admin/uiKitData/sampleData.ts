import type { PathEdge, PathNode } from '@/components/diagram/PathDiagram'
import type { GraphColumn, GraphEdge } from '@/components/diagram/RelationGraph'
import { assignLandingColors, type LandingColor } from '@/components/diagram/landingColor'

// PSP-shaped sample data for the dev-only data/form/diagram gallery. Nothing
// here ships: the module is only reachable through /__ui-kit/data.

export type NodeStatus = 'up' | 'down' | 'pending'

export interface NodeRow {
  id: string
  name: string
  region: string
  protocol: string
  status: NodeStatus
  group: string
  trafficGb: number
  lines: number
}

const REGIONS = ['US-Irvine', 'JP-Tokyo', 'HK', 'SG', 'TW-Taipei', 'DE-Frankfurt']
const PROTOCOLS = ['VLESS Reality', 'Shadowsocks 2022', 'Trojan', 'Hysteria2', 'VMess']
const GROUPS = ['Premium', 'Standard', 'Trial', 'Staff']

export const NODES: NodeRow[] = Array.from({ length: 38 }, (_, i) => ({
  id: `n${i + 1}`,
  name: i === 0 ? 'Irvine Home' : i === 1 ? 'Taiwan HiNet Dynamic' : `${REGIONS[i % REGIONS.length]} ${String(i + 1).padStart(2, '0')}`,
  region: REGIONS[i % REGIONS.length],
  protocol: PROTOCOLS[i % PROTOCOLS.length],
  status: i % 9 === 4 ? 'down' : i % 7 === 3 ? 'pending' : 'up',
  group: GROUPS[i % GROUPS.length],
  trafficGb: Math.round(((i * 53) % 400) * 1.7 * 10) / 10,
  lines: (i % 4) + 1,
}))

export type LineKind = 'forward' | 'tunnel' | 'cdn' | 'external' | 'direct'
export type LineStatus = 'ok' | 'pending' | 'down'

/** A row of the subscription-order list: a line, or a separator the subscriber sees between groups of lines. */
export interface LineRow {
  id: string
  kind: 'line' | 'separator'
  /** Separator text. */
  title?: string
  landingId: number
  landing: string
  lineKind: LineKind
  entry: string
  port: number
  latencyMs: number | null
  status: LineStatus
  multiplier: number
  /** Hidden from subscribers: shown dimmed. */
  hidden?: boolean
}

const LANDINGS = ['Irvine Home', 'Tokyo Edge', 'Frankfurt 01']
const ENTRIES = ['Taiwan HiNet Dynamic', 'Hong Kong CN2', 'Singapore Relay', 'Cloudflare', 'External entry']
const sep = (id: string, title: string): LineRow => ({ id, kind: 'separator', title, landingId: 0, landing: '', lineKind: 'direct', entry: '', port: 0, latencyMs: null, status: 'ok', multiplier: 1 })

export const LINES: LineRow[] = [
  { id: 'l1', kind: 'line', landingId: 3, landing: LANDINGS[0], lineKind: 'forward', entry: ENTRIES[0], port: 10443, latencyMs: 48, status: 'ok', multiplier: 1 },
  { id: 'l2', kind: 'line', landingId: 3, landing: LANDINGS[0], lineKind: 'tunnel', entry: ENTRIES[1], port: 20443, latencyMs: 61, status: 'ok', multiplier: 1.5 },
  { id: 'l3', kind: 'line', landingId: 3, landing: LANDINGS[0], lineKind: 'direct', entry: LANDINGS[0], port: 443, latencyMs: 142, status: 'ok', multiplier: 1 },
  sep('sep1', 'Tokyo 线路'),
  { id: 'l4', kind: 'line', landingId: 7, landing: LANDINGS[1], lineKind: 'forward', entry: ENTRIES[2], port: 30443, latencyMs: null, status: 'pending', multiplier: 1 },
  { id: 'l5', kind: 'line', landingId: 7, landing: LANDINGS[1], lineKind: 'cdn', entry: ENTRIES[3], port: 443, latencyMs: 88, status: 'ok', multiplier: 1 },
  { id: 'l6', kind: 'line', landingId: 7, landing: LANDINGS[1], lineKind: 'external', entry: ENTRIES[4], port: 8443, latencyMs: 210, status: 'down', multiplier: 2 },
  sep('sep2', 'Frankfurt 线路'),
  { id: 'l7', kind: 'line', landingId: 11, landing: LANDINGS[2], lineKind: 'tunnel', entry: ENTRIES[1], port: 21443, latencyMs: 96, status: 'ok', multiplier: 1 },
  { id: 'l8', kind: 'line', landingId: 11, landing: LANDINGS[2], lineKind: 'direct', entry: LANDINGS[2], port: 443, latencyMs: 156, status: 'ok', multiplier: 1, hidden: true },
]

/** Colours for this page's landings, by first appearance in the list. */
export function landingColorsFor(lines: readonly LineRow[]): Map<string, LandingColor> {
  return assignLandingColors(lines.filter(l => l.kind === 'line').map(l => l.landingId))
}

const TONE = { ok: 'success', pending: 'warning', down: 'danger' } as const

/** The user-to-internet path a line draws, as the nodes page will build it. */
export function linePath(line: LineRow, withEnds: boolean, colors: Map<string, LandingColor>): { nodes: PathNode[]; edges: PathEdge[] } {
  const tone = TONE[line.status]
  const landing: PathNode = { id: `landing-${line.landingId}`, kind: 'landing', label: line.landing, landingColor: colors.get(String(line.landingId)), status: tone }
  const user: PathNode = { id: 'user', kind: 'user', label: '用户' }
  const internet: PathNode = { id: 'internet', kind: 'internet', label: '互联网' }
  const latency = line.latencyMs === null ? undefined : `${line.latencyMs} ms`
  const waiting = line.status === 'pending' ? '等待生效' : undefined
  const label = waiting ?? latency
  let nodes: PathNode[]
  let edges: PathEdge[]
  switch (line.lineKind) {
    case 'direct':
      nodes = [user, { ...landing, sublabel: `:${line.port}` }]
      edges = [{ style: 'solid', label, status: tone, name: '' }]
      break
    case 'cdn':
      nodes = [user, { id: 'cdn', kind: 'cdn', label: line.entry, status: tone }, landing]
      edges = [{ style: 'solid' }, { style: 'solid', name: '回源', label, status: tone }]
      break
    case 'external':
      nodes = [user, { id: 'ext', kind: 'external', label: line.entry, sublabel: `:${line.port}`, status: tone }, landing]
      edges = [{ style: 'solid' }, { style: 'solid', name: '', label, status: tone }]
      break
    case 'tunnel':
      nodes = [user, { id: 'entry', kind: 'entry', label: line.entry, sublabel: `:${line.port}`, status: tone }, landing]
      edges = [{ style: 'solid' }, { style: 'dashed', label, status: tone }]
      break
    default:
      nodes = [user, { id: 'entry', kind: 'entry', label: line.entry, sublabel: `:${line.port}`, status: tone }, landing]
      edges = [{ style: 'solid' }, { style: 'solid', label, status: tone }]
  }
  if (withEnds) {
    nodes = [...nodes, internet]
    edges = [...edges, { style: 'solid', name: '' }]
  }
  return { nodes, edges }
}

export interface UserRow {
  id: number
  upn: string
  name: string
  group: string
  usedGb: number
  limitGb: number
  expires: string
  enabled: boolean
}

export const USERS: UserRow[] = Array.from({ length: 137 }, (_, i) => ({
  id: i + 1,
  upn: `user${String(i + 1).padStart(3, '0')}@example.com`,
  name: ['Mei Lin', 'Hiro Tanaka', 'Anna Weber', 'Chen Yu', 'Sam Ortiz', 'Ji-woo Park'][i % 6],
  group: GROUPS[i % GROUPS.length],
  usedGb: Math.round(((i * 29) % 300) * 1.3 * 10) / 10,
  limitGb: [100, 200, 500, 0][i % 4],
  expires: `2027-${String((i % 12) + 1).padStart(2, '0')}-${String((i % 27) + 1).padStart(2, '0')}`,
  enabled: i % 11 !== 5,
}))

export interface UserQuery { page: number; pageSize: number; sortBy: string; sortDesc: boolean; keyword: string }

/** A pretend paged endpoint: sorts, filters and slices, after a delay, like the real list APIs. */
export function fetchUsers(q: UserQuery, opts: { fail?: boolean; empty?: boolean } = {}): Promise<{ items: UserRow[]; total: number }> {
  return new Promise((resolve, reject) => {
    setTimeout(() => {
      if (opts.fail) { reject(new Error('502 Bad Gateway from /api/admin/users')); return }
      const base = opts.empty ? [] : USERS
      const kw = q.keyword.trim().toLowerCase()
      let rows = kw ? base.filter(u => `${u.upn} ${u.name} ${u.group}`.toLowerCase().includes(kw)) : base.slice()
      if (q.sortBy) {
        const key = q.sortBy as keyof UserRow
        rows = rows.sort((a, b) => (a[key] < b[key] ? -1 : a[key] > b[key] ? 1 : 0) * (q.sortDesc ? -1 : 1))
      }
      const start = (q.page - 1) * q.pageSize
      resolve({ items: rows.slice(start, start + q.pageSize), total: rows.length })
    }, 450)
  })
}

export function graphSample(perColumn: number): { columns: GraphColumn[]; edges: GraphEdge[] } {
  const colors = assignLandingColors(Array.from({ length: perColumn }, (_, i) => i))
  const entries = Array.from({ length: perColumn }, (_, i) => ({
    id: `e${i}`, label: i % 3 === 0 ? `Entry ${i + 1} HiNet` : `Entry ${i + 1}`, sublabel: `:${10000 + i * 7}`,
    status: (i % 11 === 4 ? 'danger' : i % 7 === 3 ? 'warning' : 'success') as 'danger' | 'warning' | 'success',
  }))
  const landings = Array.from({ length: perColumn }, (_, i) => ({
    id: `l${i}`, label: `Landing ${i + 1}`, sublabel: ['Irvine', 'Tokyo', 'Frankfurt'][i % 3], landingColor: colors.get(String(i))!,
    status: 'success' as const,
  }))
  const exits = Array.from({ length: Math.max(3, Math.ceil(perColumn / 3)) }, (_, i) => ({
    id: `x${i}`, label: `Exit ${i + 1}`, sublabel: 'direct',
  }))
  const edges: GraphEdge[] = []
  entries.forEach((e, i) => {
    edges.push({ from: e.id, to: `l${(i * 5) % perColumn}`, style: i % 4 === 0 ? 'dashed' : 'solid', status: e.status })
    if (i % 3 === 0) edges.push({ from: e.id, to: `l${(i * 5 + 7) % perColumn}`, style: 'solid', status: 'success' })
  })
  landings.forEach((l, i) => edges.push({ from: l.id, to: `x${i % exits.length}`, style: 'solid', status: 'neutral' }))
  return {
    columns: [
      { id: 'entries', title: '入口服务器', boxes: entries },
      { id: 'landings', title: '落地节点', boxes: landings },
      { id: 'exits', title: '出口', boxes: exits },
    ],
    edges,
  }
}
