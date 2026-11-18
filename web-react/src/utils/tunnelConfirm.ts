import { frontErrorOf, type FrontError } from '@/api/fronts'
import { confirm } from '@/components/ConfirmHost'
import i18n from '@/i18n'

interface CreatedListener {
  line_id?: string; line_name?: string; landing_name?: string
  /** The entry server and the listener port that opens on it. */
  entry_panel_id?: number; port?: number
  /** The landing's server and the tunnel port; the port opens only when the tunnel is new. */
  exit_panel_id?: number; link_port?: number; link_new?: boolean
  /** 'forward': a tunnel line going back to a direct forward, which opens `forward_port` on the entry server. */
  kind?: 'forward'; forward_port?: number
}
interface RemovedListener { node_id?: number; name?: string; line_id?: string; line_name?: string; entry_panel_id?: number; port?: number; front_port?: number }

// Server names for the confirmation text, kept by the page that lists the servers.
let serverNames = new Map<number, string>()

/** Tells the confirmations which servers exist, so a port is named with its server. */
export function registerServerNames(servers: readonly { id: number; name: string }[]) {
  serverNames = new Map(servers.map(s => [s.id, s.name]))
}
export interface TunnelDiffItem { field: string; from: string; to: string }

const ACTIONS = ['switch_mode', 'create_line', 'remove_line', 'delete_node', 'sync_from_landing']

const list = <T,>(v: unknown): T[] => (Array.isArray(v) ? v as T[] : [])

/** True for the 409 a tunnel change answers before it creates or removes a listener. */
export function isTunnelConfirmation(e: FrontError | null): e is FrontError {
  return !!e && e.status === 409 && e.code === 'confirm_required' && e.params.kind === 'tunnel'
}

/** The confirmation text for a tunnel refusal: what is created, what is removed, and that subscribers must refresh. */
export function tunnelConfirmMessage(e: FrontError): string {
  const t = (key: string, values?: Record<string, unknown>) => i18n.t(`admin:nodes.tunnel.confirm.${key}`, values) as string
  const action = String(e.params.action ?? '')
  const lines: string[] = [t(`${action}_text`)]
  const server = (id?: number) => serverNames.get(id ?? 0) ?? t('server_fallback', { id: id ?? 0 })
  for (const c of list<CreatedListener>(e.params.creates)) {
    if (c.kind === 'forward') {
      lines.push(t('creates_forward', { line: c.line_name ?? c.line_id ?? '', landing: c.landing_name ?? '', port: c.forward_port ?? 0, entry: server(c.entry_panel_id) }))
      continue
    }
    lines.push(t('creates_item', { line: c.line_name ?? c.line_id ?? '', landing: c.landing_name ?? '', port: c.port ?? 0, entry: server(c.entry_panel_id) }))
    if (c.link_new) lines.push(t('creates_link', { port: c.link_port ?? 0, exit: server(c.exit_panel_id), entry: server(c.entry_panel_id) }))
  }
  for (const r of list<RemovedListener>(e.params.removes)) {
    lines.push(t(r.front_port ? 'removes_item_front' : 'removes_item', {
      name: r.name ?? `#${r.node_id ?? 0}`, line: r.line_name ?? r.line_id ?? '', port: r.port ?? 0, front_port: r.front_port ?? 0,
      entry: server(r.entry_panel_id),
    }))
  }
  for (const d of list<TunnelDiffItem>(e.params.diff)) {
    lines.push(t('diff_item', { field: d.field, from: d.from, to: d.to }))
  }
  if (action === 'sync_from_landing') lines.push(t('sync_keeps'))
  if (e.params.refresh_needed) lines.push(t('refresh'))
  return lines.join('\n')
}

/** What a confirmed resend echoes back: the server's plan the admin was shown. */
export interface TunnelConfirmation { plan: string }

/**
 * Runs a change and, when the server answers 409 `confirm_required` because it
 * would create or remove tunnel listeners (switching a line's mode, creating or
 * removing a tunnel line, deleting a landing that has some), asks the admin in
 * words that name each listener and port and say subscribers must refresh, then
 * runs the same change again echoing the plan it showed. If the plan changed in
 * between, the server answers a new 409 and the admin is asked again, with the
 * fresh ports. Declining throws the refusal being asked about, so nothing is
 * changed. Other refusals pass through untouched. `note` is added to the text.
 */
export async function withTunnelConfirm<T>(run: (confirmation?: TunnelConfirmation) => Promise<T>, note?: string): Promise<T> {
  let confirmation: TunnelConfirmation | undefined
  for (;;) {
    try {
      return await run(confirmation)
    } catch (err) {
      const e = frontErrorOf(err)
      if (!isTunnelConfirmation(e)) throw err
      const action = String(e.params.action ?? '')
      if (!ACTIONS.includes(action)) throw err
      const plan = typeof e.params.plan === 'string' ? e.params.plan : ''
      // The same plan refused after it was confirmed is not something asking again can fix.
      if (confirmation && confirmation.plan === plan) throw err
      const ok = await confirm({
        title: i18n.t(`admin:nodes.tunnel.confirm.${action}_title`) as string,
        message: [tunnelConfirmMessage(e), note].filter(Boolean).join('\n'),
        destructive: true,
      })
      if (!ok) throw err
      confirmation = { plan }
    }
  }
}
