import type { NodeSetupEntry } from '@/api/nodes'
import type { RelayLine } from '@/api/types'

/**
 * One entry of the create wizard: the line as it will be sent, plus the tunnel listener's three fields while the
 * line is an encrypted tunnel (the server makes the listener's keys; only these are chosen at setup).
 */
export interface SetupRelay {
  key: string
  line: RelayLine
  tunnel?: NodeSetupEntry
}

export const newSetupRelay = (key: string): SetupRelay => ({
  key, line: { name: '', address: '', port: 0, enabled: true },
})

export type SetupField = 'address' | 'port' | 'domain' | 'sni' | 'target'
/** Each problem is a locale key under `nodes.`, the same kind of key the line editor's validation gives. */
export type SetupProblems = Partial<Record<SetupField, string>>

/**
 * What stops an entry from being planned. The inputs also carry the native `required` attribute, so a wizard
 * that submits through a plain form is stopped by the browser too; this is what the fields say once left.
 * A managed entry (one with an entry server) needs its port: the server cannot pick the public port of a
 * forward on its own, whereas an external entry's port 0 means "the landing's port".
 */
export function setupProblems({ line, tunnel }: SetupRelay): SetupProblems {
  const out: SetupProblems = {}
  if (line.mode === 'cdn') {
    if (!line.domain?.trim()) out.domain = 'cdn.domain_required'
    if (line.port > 65535) out.port = 'line.err.port_range'
    return out
  }
  if (!line.address.trim()) out.address = 'line.err.address_required'
  if (line.entry_panel_id && !(line.port > 0)) out.port = 'line.err.port_required'
  if (line.port > 65535) out.port = 'line.err.port_range'
  if (line.mode === 'tunnel' && tunnel) {
    if (!tunnel.sni.trim()) out.sni = 'setup.field_required'
    if (!tunnel.target.trim()) out.target = 'setup.field_required'
  }
  return out
}

export const setupEntriesValid = (entries: readonly SetupRelay[]) => entries.every(e => Object.keys(setupProblems(e)).length === 0)
