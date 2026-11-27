import type { Node } from '@/api/types'
import type { AdminInbound } from '@/api/nodes'
import { type InboundFormState, SS2022_METHODS, parseJSONSafe } from './inboundModel'
import { type InboundDetail, parseInboundForEdit } from './inboundSettings'

type T = (key: string) => string

export type InboundRead =
  | { kind: 'ready'; form: InboundFormState; /** The config is not what the server runs yet, or the read had an error: say so. */ syncNotice: boolean }
  /** Something is wrong with what was read: retry, do not show editable defaults. */
  | { kind: 'error'; message: string }
  /** A protocol or a Shadowsocks method this form cannot represent: refusing is the only safe answer, a save would rewrite it. */
  | { kind: 'unsupported'; reason: 'protocol' | 'ss_method' }

const SUPPORTED = new Set(['vless', 'shadowsocks', 'vmess', 'trojan', 'hysteria2', 'anytls', 'tuic', 'naive'])

/**
 * What `GET /nodes/:id` says of a node's inbound, as the form's starting state or the reason there is none. It
 * is strict on purpose: a metadata row is not an inbound, and the forgiving parser (`parseInboundForEdit`) is
 * only safe after the shape was checked, so a malformed snapshot is a read failure to retry, never an
 * "unsupported protocol" and never a form of defaults that a save would write over the real inbound.
 */
export function readInbound(detail: { node: Node; inbound?: AdminInbound; inbound_error?: string }, nodeId: number, t: T): InboundRead {
  const invalid = () => ({ kind: 'error', message: t('admin:nodes.edit_inbound_dialog.invalid_configuration') }) as const
  try {
    if (detail.node?.id !== nodeId) return invalid()
    const ib = detail.inbound
    if (!ib) return { kind: 'error', message: detail.inbound_error || t('admin:nodes.edit_inbound_dialog.missing_configuration') }
    if (typeof ib.protocol !== 'string' || !ib.protocol.trim() || typeof ib.port !== 'number' || !Number.isInteger(ib.port) || ib.port < 1 || ib.port > 65535) return invalid()
    if (!SUPPORTED.has(ib.protocol)) return { kind: 'unsupported', reason: 'protocol' }
    for (const raw of [ib.settings, ib.stream_settings, ib.sniffing]) {
      if (typeof raw !== 'string') return invalid()
      const value: unknown = JSON.parse(raw || '{}')
      if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid()
    }
    if (ib.protocol === 'shadowsocks') {
      const method = parseJSONSafe(ib.settings).method
      if (!SS2022_METHODS.some(m => m.value === method)) return { kind: 'unsupported', reason: 'ss_method' }
    }
    return {
      kind: 'ready',
      form: parseInboundForEdit(detail.node, ib as InboundDetail),
      syncNotice: ['pending', 'drift', 'failed'].includes(detail.node.config_sync_state || '') || !!detail.inbound_error,
    }
  } catch (err) {
    return { kind: 'error', message: (err as { message?: string }).message || t('admin:nodes.edit_inbound_dialog.missing_configuration') }
  }
}
