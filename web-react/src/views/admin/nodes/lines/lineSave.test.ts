// @vitest-environment jsdom
import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Node, RelayLine } from '@/api/types'
import { draftOf } from './lineModel'
import { saveLineChange } from './lineSave'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// What GET /nodes/:id answers for a node with a loaded state on every line: the save must send none of it back.
const forward: RelayLine = {
  id: 'F1', name: 'GZ', address: 'gz.example', port: 20443, enabled: true, entry_panel_id: 7, region: 'CN', tags: ['cmcc'],
  traffic_factor_milli: 1500, managed_ready: true, probe: { state: 'ok', median_rtt_ms: 30, loss_percent: 0, withheld: false }, source_cidrs: ['203.0.113.0/24'],
}
const tunnel: RelayLine = {
  id: 'T1', name: 'SH', address: 'sh.example', port: 31002, enabled: true, entry_panel_id: 8, mode: 'tunnel', running: true,
  tunnel: { node_id: 77, name: 'tunnel-77', enabled: true, port: 31002, protocol: 'vless', flow: '' } as RelayLine['tunnel'],
}
const fresh = (relays: RelayLine[]): Node => ({
  id: 1, panel_id: 1, display_name: 'Landing', server_address: 'land.example', region: 'TW', tags: ['premium'], sort_order: 40,
  flow: 'xtls-rprx-vision', hide_direct: true, egress_panel_id: 9, traffic_factor_milli: 2000, enabled: true, relays,
} as Node)

const metadataPuts = () => api.put.mock.calls.filter(([url]) => url === '/admin/nodes/1/metadata')
const refusal = (data: Record<string, unknown>) => new AxiosError('x', '409', undefined, undefined, { status: 409, statusText: '', headers: {}, config: {} as never, data })
const tunnelRefusal = (action: string) => refusal({ code: 'confirm_required', params: { kind: 'tunnel', action, plan: 'p1', creates: [], removes: [{ node_id: 77, name: 'tunnel-77', line_name: 'SH', port: 31002, entry_panel_id: 8 }], refresh_needed: true } })

function serve(relays: RelayLine[]) {
  const node = fresh(relays)
  api.get.mockResolvedValue({ data: { node, clients: [] } })
  api.put.mockImplementation(async (_u: string, body: Partial<Node>) => ({ data: { ...node, ...body } }))
  return node
}

beforeEach(() => vi.clearAllMocks())

describe('saveLineChange', () => {
  it('adds a line to the FRESH relays and sends only flow, relays and hide_direct', async () => {
    serve([forward, tunnel])
    const draft = { ...draftOf({ name: 'CF', address: '', port: 443, enabled: true, mode: 'cdn', domain: ' cdn.example.com ' }) }
    const { saved, addedId } = await saveLineChange(1, { kind: 'save', draft })
    expect(api.get).toHaveBeenCalledWith('/admin/nodes/1', expect.anything())
    expect(metadataPuts()).toHaveLength(1)
    const body = metadataPuts()[0][1] as Record<string, unknown> & { relays: Record<string, unknown>[] }
    // Exactly these keys: no name, address, region, tags, egress, sort_order, direct factor.
    expect(Object.keys(body).sort()).toEqual(['flow', 'hide_direct', 'relays'])
    expect(body.flow).toBe('xtls-rprx-vision')
    expect(body.hide_direct).toBe(true)
    expect(body.relays).toHaveLength(3)
    // Untouched lines keep their id and labels; what the server decides is not sent back, nor their factor.
    expect(body.relays[0]).toMatchObject({ id: 'F1', name: 'GZ', region: 'CN', tags: ['cmcc'], source_cidrs: ['203.0.113.0/24'], entry_panel_id: 7 })
    for (const k of ['managed_ready', 'probe', 'running', 'tunnel', 'traffic_factor_milli']) { expect(body.relays[0]).not.toHaveProperty(k); expect(body.relays[1]).not.toHaveProperty(k) }
    expect(body.relays[1]).toMatchObject({ id: 'T1', mode: 'tunnel', entry_panel_id: 8 })
    expect(body.relays[2]).toMatchObject({ mode: 'cdn', domain: 'cdn.example.com', address: '', port: 443 })
    expect(body.relays[2]).not.toHaveProperty('id')
    expect(saved.id).toBe(1)
    expect(addedId).toBeUndefined() // the mocked server echoes the request, which has no id for it
  })

  it('keeps a line that was read without labels free of label fields, so the server keeps its own', async () => {
    serve([{ id: 'L1', name: 'Old', address: 'old.example', port: 0, enabled: true }])
    await saveLineChange(1, { kind: 'remove', lineId: 'none' })
    const relay = (metadataPuts()[0][1] as { relays: Record<string, unknown>[] }).relays[0]
    expect(relay).not.toHaveProperty('region')
    expect(relay).not.toHaveProperty('tags')
  })

  it('replaces the edited line in place and leaves the others as read', async () => {
    serve([forward, tunnel])
    const draft = { ...draftOf(forward), port: 20444, _factor_text: '0.5', _factor_touched: true, traffic_factor_milli: 500 }
    await saveLineChange(1, { kind: 'save', draft })
    const relays = (metadataPuts()[0][1] as { relays: Record<string, unknown>[] }).relays
    expect(relays.map(r => r.id)).toEqual(['F1', 'T1'])
    expect(relays[0]).toMatchObject({ port: 20444, traffic_factor_milli: 500 })
    expect(relays[1]).not.toHaveProperty('traffic_factor_milli')
  })

  it('sends a factor only after it was edited, and never a tunnel line\'s source addresses', async () => {
    serve([forward, tunnel])
    await saveLineChange(1, { kind: 'save', draft: { ...draftOf(tunnel), name: 'SH2', _source_text: '198.51.100.0/24' } })
    const relays = (metadataPuts()[0][1] as { relays: Record<string, unknown>[] }).relays
    expect(relays[1]).toMatchObject({ id: 'T1', name: 'SH2' })
    expect(relays[1].source_cidrs).toBeUndefined()
    expect(relays[0]).not.toHaveProperty('traffic_factor_milli')
  })

  it('enable and disable change only the enabled flag of that line', async () => {
    serve([forward, tunnel])
    await saveLineChange(1, { kind: 'toggle', lineIds: ['F1'], enabled: false })
    const relays = (metadataPuts()[0][1] as { relays: Record<string, unknown>[] }).relays
    expect(relays[0]).toMatchObject({ id: 'F1', enabled: false, address: 'gz.example', port: 20443 })
    expect(relays[1]).toMatchObject({ id: 'T1', enabled: true })
  })

  it('delete drops that line and keeps hide_direct and flow', async () => {
    serve([forward, tunnel])
    await saveLineChange(1, { kind: 'remove', lineId: 'F1' })
    const body = metadataPuts()[0][1] as { relays: { id: string }[]; hide_direct: boolean; flow: string }
    expect(body.relays.map(r => r.id)).toEqual(['T1'])
    expect(body.hide_direct).toBe(true)
    expect(body.flow).toBe('xtls-rprx-vision')
  })

  it('the direct line sends the fresh relays unchanged with its own switch and factor', async () => {
    serve([forward])
    await saveLineChange(1, { kind: 'direct', hidden: false, factor: { text: '2.5', milli: 2500 } })
    const body = metadataPuts()[0][1] as Record<string, unknown> & { relays: Record<string, unknown>[] }
    expect(body).toMatchObject({ flow: 'xtls-rprx-vision', hide_direct: false, traffic_factor_milli: 2500 })
    expect(body.relays).toHaveLength(1)
    expect(body.relays[0]).toMatchObject({ id: 'F1', enabled: true })
    expect(Object.keys(body).sort()).toEqual(['flow', 'hide_direct', 'relays', 'traffic_factor_milli'])
  })

  it('shows the server\'s tunnel plan through the presenter and resends with the confirmation, one dialog only', async () => {
    const node = serve([tunnel])
    api.put.mockRejectedValueOnce(tunnelRefusal('remove_line')).mockImplementation(async (_u: string, body: Partial<Node>) => ({ data: { ...node, ...body } }))
    const present = vi.fn().mockResolvedValue(true)
    await saveLineChange(1, { kind: 'remove', lineId: 'T1' }, { present })
    expect(present).toHaveBeenCalledTimes(1)
    expect(present.mock.calls[0][0].action).toBe('remove_line')
    expect(metadataPuts()).toHaveLength(2)
    expect(metadataPuts()[1][2]).toEqual({ params: { confirm_tunnels: 'true', confirm_plan: 'p1' } })
  })

  it('declining the plan sends nothing more and rejects', async () => {
    serve([tunnel])
    api.put.mockRejectedValue(tunnelRefusal('remove_line'))
    await expect(saveLineChange(1, { kind: 'remove', lineId: 'T1' }, { present: async () => false })).rejects.toBeTruthy()
    expect(metadataPuts()).toHaveLength(1)
  })

  it('reports the id of the line a save added', async () => {
    const node = serve([forward])
    api.put.mockImplementation(async (_u: string, body: { relays: RelayLine[] }) => ({ data: { ...node, relays: [forward, { ...body.relays[1], id: 'N9' }] } }))
    const { addedId } = await saveLineChange(1, { kind: 'save', draft: draftOf({ name: 'x', address: 'x.example', port: 1, enabled: true }) })
    expect(addedId).toBe('N9')
  })
})
