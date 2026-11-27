import type { Node, RelayLine } from '@/api/types'
import { api, installReads, list } from '../testHarness'

// Shared by the tests of the line editor: a landing node with three servers, and the reads it needs. Not part of the app.
export const servers = [
  { id: 1, name: 'Landing server', url: 'https://land.example', address: 'land.example', panel_type: 'psp', capabilities: ['inbound.enable'] },
  { id: 2, name: 'Entry GZ', url: 'https://gz.example', address: 'gz.example', panel_type: 'psp', capabilities: ['inbound.enable'] },
  { id: 3, name: 'Entry SH', url: 'https://sh.example:8443', panel_type: 'psp', capabilities: ['inbound.enable'] },
]

export const forward: RelayLine = { id: 'F1', name: 'GZ', address: 'gz.example', port: 20443, enabled: true, entry_panel_id: 2, managed_ready: true, region: 'CN', tags: ['cmcc'], traffic_factor_milli: 1500 }
export const listener = {
  node_id: 77, name: 'tunnel-77', enabled: true, port: 31002, protocol: 'vless', flow: '', security: 'reality', server_names: ['www.asus.com'], target: 'www.asus.com:443',
  short_ids: ['ab12'], public_key: 'PUB', private_key: 'PRIV', applied: true, egress_ready: true, routes: 0, config_sync_state: 'synced',
} as NonNullable<RelayLine['tunnel']>
export const tunnel: RelayLine = { id: 'T1', name: 'SH', address: 'sh.example', port: 31002, enabled: true, entry_panel_id: 3, mode: 'tunnel', running: true, tunnel: listener }

export const landing = (relays: RelayLine[] = [], over: Partial<Node> = {}) => ({
  id: 1, panel_id: 1, panel_name: 'Landing server', inbound_id: 5, display_name: 'Landing', server_address: 'land.example', region: 'TW', tags: ['premium'], sort_order: 10,
  enabled: true, flow: 'xtls-rprx-vision', hide_direct: false, traffic_factor_milli: 1000, relays, ...over,
} as Node)

/** Serves the node, its servers and the answers the editor asks for; PUT echoes the request onto the node. */
export function serve(node: Node, svs: unknown[] = servers) {
  installReads({
    '/admin/nodes/1': { node, clients: [] }, '/admin/nodes': list([node]), '/admin/servers': list(svs),
    '/admin/nodes/1/source-guard': { can_guard: true, lines: [], cloudflare: { cidrs: [], source: 'builtin', builtin_date: '2026-11-18', auto_fetch: true } },
  })
  api.post.mockResolvedValue({ data: { groups: [{ id: 1, name: 'VIP' }], node_groups: [1], saved_node_groups: [1], relays: node.relays?.map(() => [1]).concat([[1]]) } })
  api.put.mockImplementation(async (_url: string, body: Partial<Node> & { relays?: RelayLine[] }) => ({
    data: { ...node, ...body, relays: (body.relays ?? []).map((r, i) => ({ ...(node.relays ?? []).find(x => x.id === r.id), ...r, id: r.id ?? `N${i}` })) },
  }))
}

export const metadataPuts = () => api.put.mock.calls.filter(([url]) => url === '/admin/nodes/1/metadata')
