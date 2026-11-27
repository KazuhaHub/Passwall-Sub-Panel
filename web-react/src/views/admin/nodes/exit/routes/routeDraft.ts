import type { RouteInput, RouteTarget, RouteView } from '@/api/routes'

// The editor's draft of a node's routes, and the checks it can make before
// asking the server. The server is the authority and repeats every one of
// them; the checks only spare a request that is certain to be refused. A draft
// holds exactly what the admin typed: nothing is derived at save time, so what
// is sent is what is shown.

export interface DraftRoute {
  /** Stable row key for React. */
  key: string
  /** The server-assigned id; absent for a route added in this draft. */
  id?: string
  name: string
  /** `local`, or `server:<panel id>`; empty until chosen. */
  target: string
  listIds: number[]
  /** One typed entry per line. */
  domains: string
  cidrs: string
  ports: string
  network: '' | 'tcp' | 'udp'
}

let rowSeq = 0
export const newRouteKey = () => `row${++rowSeq}`

export const targetKey = {
  local: 'local',
  server: (panelId: number) => `server:${panelId}`,
}

export function emptyRoute(): DraftRoute {
  return { key: newRouteKey(), name: '', target: '', listIds: [], domains: '', cidrs: '', ports: '', network: '' }
}

const lines = (text: string) => text.split(/[\n,，;；]+/).map(s => s.trim()).filter(Boolean)

export function targetOf(key: string): RouteTarget | null {
  if (key === targetKey.local) return { kind: 'local' }
  const m = /^server:(\d+)$/.exec(key)
  return m ? { kind: 'server', panel_id: Number(m[1]) } : null
}

export function keyOfTarget(target: RouteTarget): string {
  return target.kind === 'local' ? targetKey.local : targetKey.server(target.panel_id)
}

export function draftOf(routes: readonly RouteView[]): DraftRoute[] {
  return routes.map(r => ({
    key: newRouteKey(), id: r.id, name: r.name ?? '', target: keyOfTarget(r.target),
    listIds: [...r.list_ids], domains: r.domains.join('\n'), cidrs: r.cidrs.join('\n'), ports: r.ports, network: r.network,
  }))
}

export function inputOf(draft: readonly DraftRoute[]): RouteInput[] {
  return draft.map(r => ({
    ...(r.id ? { id: r.id } : {}),
    ...(r.name.trim() ? { name: r.name.trim() } : {}),
    target: targetOf(r.target) ?? { kind: 'local' as const },
    list_ids: r.listIds,
    domains: lines(r.domains),
    cidrs: lines(r.cidrs),
    ports: r.ports.replace(/\s+/g, ''),
    network: r.network,
  }))
}

/** The draft differs from what is saved: the order, a value, an added or a removed route. */
export function isDirty(draft: readonly DraftRoute[], saved: readonly RouteView[]): boolean {
  return JSON.stringify(inputOf(draft)) !== JSON.stringify(inputOf(draftOf(saved)))
}

/** Moves a row; a position outside the list leaves it as it is. */
export function moveRoute<T>(rows: readonly T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= rows.length || to >= rows.length) return [...rows]
  const next = [...rows]
  const [row] = next.splice(from, 1)
  next.splice(to, 0, row)
  return next
}

export const NAME_MAX_BYTES = 64
const TYPED = /^(domain|full|keyword):\S+$/i
const REGEXP = /^regexp:/i
const PORTS = /^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$/
const validPort = (n: number) => n >= 1 && n <= 65535

export type DraftIssue =
  | { code: 'target'; route: number }
  | { code: 'name_long'; route: number }
  | { code: 'no_match'; route: number }
  | { code: 'regexp'; route: number; entry: string }
  | { code: 'entry'; route: number; entry: string }
  | { code: 'ports'; route: number }

export function portsValid(text: string): boolean {
  const t = text.replace(/\s+/g, '')
  if (t === '') return true
  if (!PORTS.test(t)) return false
  return t.split(',').every(part => {
    const [a, b] = part.split('-').map(Number)
    return validPort(a) && (b === undefined || (validPort(b) && a <= b))
  })
}

/** What the draft can be refused for without asking the server, in reading order. 1-based route numbers. */
export function draftIssues(draft: readonly DraftRoute[]): DraftIssue[] {
  const issues: DraftIssue[] = []
  draft.forEach((r, i) => {
    const route = i + 1
    if (!targetOf(r.target)) issues.push({ code: 'target', route })
    if (new TextEncoder().encode(r.name.trim()).length > NAME_MAX_BYTES) issues.push({ code: 'name_long', route })
    if (r.listIds.length === 0 && lines(r.domains).length === 0 && lines(r.cidrs).length === 0 && !r.ports.trim() && r.network === '') {
      issues.push({ code: 'no_match', route })
    }
    for (const entry of lines(r.domains)) {
      if (REGEXP.test(entry)) issues.push({ code: 'regexp', route, entry })
      else if (!TYPED.test(entry)) issues.push({ code: 'entry', route, entry })
    }
    if (!portsValid(r.ports)) issues.push({ code: 'ports', route })
  })
  return issues
}
