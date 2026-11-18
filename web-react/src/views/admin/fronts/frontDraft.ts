import { parseServerNames, type Front, type FrontInput, type FrontLineOption, type FrontListenerOption, type FrontOptions, type FrontRoute } from '@/api/fronts'

// The editor's draft of one shared port, and the checks the editor can make
// before asking the server. The server is the authority: it repeats every one
// of these, and also knows what only it can (the server's own address, other
// objects). A draft holds exactly what the admin typed; nothing is derived at
// save time, so what is sent is what is shown.

export interface DraftListener { node_id: number; loopback: string }

export interface DraftRoute {
  /** Stable row key for React. */
  key: string
  names: string
  /** `listener:<node>` or `forward:<node>:<line>`; empty until chosen. */
  target: string
}

export interface Draft {
  port: string
  listeners: DraftListener[]
  routes: DraftRoute[]
  host: string
  defaultPort: string
  confirm: boolean
}

let rowSeq = 0
export const newRouteKey = () => `r${++rowSeq}`

export const targetKey = {
  listener: (nodeId: number) => `listener:${nodeId}`,
  link: (linkId: number) => `link:${linkId}`,
  forward: (nodeId: number, lineId: string) => `forward:${nodeId}:${lineId}`,
}

export function draftOf(front: Front | null, defaultPort: number): Draft {
  if (!front) return { port: String(defaultPort), listeners: [], routes: [], host: '', defaultPort: '443', confirm: false }
  return {
    port: String(front.port),
    listeners: front.listeners.map(l => ({ node_id: l.node_id, loopback: String(l.loopback_port) })),
    routes: front.routes.map(r => ({
      key: newRouteKey(), names: r.server_names.join(', '),
      target: r.target.kind === 'listener' ? targetKey.listener(r.target.node_id ?? 0)
        : r.target.kind === 'link' ? targetKey.link(r.target.link_id ?? 0)
        : targetKey.forward(r.target.node_id ?? 0, r.target.line_id ?? ''),
    })),
    host: front.default.host ?? '', defaultPort: String(front.default.port ?? 443), confirm: false,
  }
}

function routeTarget(target: string): FrontRoute['target'] | null {
  const [kind, node, line] = target.split(':')
  if (kind === 'link' && Number.isSafeInteger(Number(node)) && Number(node) > 0) return { kind: 'link', link_id: Number(node) }
  if (kind === 'listener' && node) return { kind: 'listener', node_id: Number(node) }
  if (kind === 'forward' && node && line) return { kind: 'forward', node_id: Number(node), line_id: line }
  return null
}

const portOf = (text: string): number => (/^\d+$/.test(text.trim()) ? Number(text.trim()) : NaN)
const validPort = (n: number) => Number.isInteger(n) && n >= 1 && n <= 65535

export function inputOf(draft: Draft): FrontInput {
  return {
    port: portOf(draft.port),
    listeners: draft.listeners.map(l => ({ node_id: l.node_id, loopback_port: portOf(l.loopback) })),
    routes: draft.routes.map(r => ({ server_names: parseServerNames(r.names), target: routeTarget(r.target) ?? { kind: 'listener' as const } })),
    default: { host: draft.host.trim(), port: portOf(draft.defaultPort) },
    confirm_move_listeners: draft.confirm,
  }
}

/** What a save takes away from a front that exists: its listeners go back to their own ports, its relay lines stop being served. */
export function leaving(draft: Draft, front: Front | null): { listeners: number[]; lines: string[] } {
  if (!front) return { listeners: [], lines: [] }
  const portChanged = Number(draft.port) !== front.port
  const kept = new Set(draft.listeners.map(l => l.node_id))
  const keptLines = new Set(draft.routes.map(r => r.target))
  return {
    listeners: front.listeners.filter(l => portChanged || !kept.has(l.node_id)).map(l => l.node_id),
    lines: front.routes
      .filter(r => r.target.kind === 'forward' && (portChanged || !keptLines.has(targetKey.forward(r.target.node_id ?? 0, r.target.line_id ?? ''))))
      .map(r => `${r.target.node_id}/${r.target.line_id}`),
  }
}

/** Tunnels whose public endpoint this edit changes. */
export function changedLinkIDs(draft: Draft, front: Front | null): number[] {
  const before = new Set(front?.routes.flatMap(r => r.target.kind === 'link' && r.target.link_id ? [r.target.link_id] : []))
  const after = new Set(draft.routes.flatMap(r => {
    const target = routeTarget(r.target)
    return target?.kind === 'link' && target.link_id ? [target.link_id] : []
  }))
  const portChanged = front != null && front.port !== portOf(draft.port)
  return [...new Set([...before, ...after])].filter(id => portChanged || before.has(id) !== after.has(id))
}

/** The listeners that would be moved off a port they hold today: not already behind this front. */
export function newlyMoved(draft: Draft, front: Front | null): number[] {
  const already = new Set((front?.listeners ?? []).map(l => l.node_id))
  return draft.listeners.filter(l => !already.has(l.node_id)).map(l => l.node_id)
}

export type DraftIssue =
  | { code: 'port'; }
  | { code: 'loopback'; node_id: number }
  | { code: 'loopback_probe'; node_id: number }
  | { code: 'loopback_twice'; port: number }
  | { code: 'route_target'; route: number }
  | { code: 'route_names'; route: number }
  | { code: 'name_twice'; name: string; route: number; owner: string }
  | { code: 'listener_unrouted'; node_id: number }
  | { code: 'default_host' }
  | { code: 'default_port' }

const inProbeRange = (n: number, low: number, high: number) => n >= low && n <= high

/** What the draft can be refused for without asking the server, in reading order. */
export function draftIssues(
  draft: Draft, options: FrontOptions | undefined, probe: { low: number; high: number },
  describe: (target: string) => string,
): DraftIssue[] {
  const issues: DraftIssue[] = []
  const port = portOf(draft.port)
  if (!validPort(port) || inProbeRange(port, probe.low, probe.high)) issues.push({ code: 'port' })
  const seenLoopback = new Set<number>()
  for (const l of draft.listeners) {
    const lb = portOf(l.loopback)
    if (!validPort(lb) || lb === port) issues.push({ code: 'loopback', node_id: l.node_id })
    else if (inProbeRange(lb, probe.low, probe.high)) issues.push({ code: 'loopback_probe', node_id: l.node_id })
    else if (seenLoopback.has(lb)) issues.push({ code: 'loopback_twice', port: lb })
    seenLoopback.add(lb)
  }
  const owner = new Map<string, string>()
  draft.routes.forEach((r, index) => {
    if (!routeTarget(r.target)) issues.push({ code: 'route_target', route: index + 1 })
    const names = parseServerNames(r.names).map(n => n.toLowerCase().replace(/\.$/, ''))
    if (names.length === 0) issues.push({ code: 'route_names', route: index + 1 })
    for (const name of names) {
      const previous = owner.get(name)
      if (previous !== undefined) issues.push({ code: 'name_twice', name, route: index + 1, owner: previous })
      else owner.set(name, describe(r.target))
    }
  })
  const routed = new Set(draft.routes.map(r => r.target))
  for (const l of draft.listeners) {
    if (!routed.has(targetKey.listener(l.node_id))) issues.push({ code: 'listener_unrouted', node_id: l.node_id })
  }
  if (draft.host.trim() === '') issues.push({ code: 'default_host' })
  if (!validPort(portOf(draft.defaultPort))) issues.push({ code: 'default_port' })
  void options
  return issues
}

/** The listeners the editor lists for a port: those on it, those already behind this front, frontable or not. */
export function listenersFor(options: FrontOptions | undefined, port: number, draft: Draft): FrontListenerOption[] {
  const chosen = new Set(draft.listeners.map(l => l.node_id))
  return (options?.listeners ?? []).filter(l => l.port === port || chosen.has(l.node_id))
}

/** The relay lines that enter on the port, so they can be a route's target. */
export function linesFor(options: FrontOptions | undefined, port: number): FrontLineOption[] {
  return (options?.lines ?? []).filter(l => l.port === port)
}
