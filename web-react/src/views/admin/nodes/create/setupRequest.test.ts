import { describe, expect, it } from 'vitest'
import type { NodeSetupPlan } from '@/api/nodes'
import { EMPTY_INBOUND } from '../inbound/inboundModel'
import { buildSetupRequest, setupOptions } from './setupRequest'

const form = { ...EMPTY_INBOUND, panel_id: 1, display_name: 'Taipei', server_address: 'tpe.example', region: 'TW', tags_text: 'a, b,,c', private_key: 'k', public_key: 'p', short_ids_text: 'ab', reality_dest: 'x.com:443', reality_server_names_text: 'x.com' }
const base = { form, panelType: '3xui' as const, json: false, entries: [], direct: true, egress: 0, options: { loopbacks: {}, tunnels: {} } }

describe('the create request', () => {
  it('carries the node, the inbound documents and the setup options, and never a sort_order', () => {
    const body = buildSetupRequest({ ...base, entries: [{ key: 'n1', line: { name: ' GZ ', address: ' gz.example ', port: 20443, enabled: true, entry_panel_id: 2 } }], direct: false, egress: 3 })
    expect(body).toMatchObject({
      panel_id: 1, display_name: 'Taipei', server_address: 'tpe.example', region: 'TW', tags: ['a', 'b', 'c'], flow: 'xtls-rprx-vision', hide_direct: true, egress_panel_id: 3,
      relays: [{ name: 'GZ', address: 'gz.example', port: 20443, entry_panel_id: 2 }],
      inbound: { remark: 'Taipei', protocol: 'vless', port: 443, allocate: '', expiry_time: 0 },
    })
    expect('sort_order' in body).toBe(false)
    expect(() => JSON.parse(body.inbound.stream_settings)).not.toThrow()
  })

  it('sends the raw documents in JSON mode, and empty sniffing for S-UI', () => {
    const raw = { ...form, raw_settings: '{"a":1}', raw_stream_settings: '{"b":2}', raw_sniffing: '{"c":3}' }
    expect(buildSetupRequest({ ...base, form: raw, json: true }).inbound).toMatchObject({ settings: '{"a":1}', stream_settings: '{"b":2}', sniffing: '{"c":3}' })
    expect(buildSetupRequest({ ...base, panelType: 'sui' }).inbound).toMatchObject({ enable: true, sniffing: '{}' })
  })

  it('binds a PSP-managed certificate only when one is chosen', () => {
    expect(buildSetupRequest({ ...base, form: { ...form, tls_cert_mode: 'psp_managed', cert_id: 7 } })).toMatchObject({ cert_source: 'psp_managed', cert_id: 7 })
    expect(buildSetupRequest({ ...base, form: { ...form, tls_cert_mode: 'psp_managed', cert_id: 0 } }).cert_source).toBeUndefined()
  })
})

describe('the options of the second request', () => {
  const plan: NodeSetupPlan = { token: 't', bindings: [
    { panel_id: 2, kind: 'forward', name: 'f', port: 20443, loopback: 40001 },
    { panel_id: 1, peer_id: 9, kind: 'link', name: '', port: 31001 },
    { panel_id: 1, peer_id: 10, kind: 'link', name: '', port: 31002, reused: true },
  ] }
  it('asks again for exactly the reviewed ports, with the edited ones winning, and skips a reused link', () => {
    const o = setupOptions({ entries: [], plan, loopbacks: { 2: 40999 }, linkPorts: { '9/1': 31999 }, routes: [] })
    expect(o.loopbacks).toEqual({ 2: 40999 })
    expect(o.link_ports).toEqual({ '9/1': 31999 })
    expect(setupOptions({ entries: [], plan, loopbacks: {}, linkPorts: {}, routes: [] })).toMatchObject({ loopbacks: { 2: 40001 }, link_ports: { '9/1': 31001 } })
  })
  it('keys the tunnel listener fields by the entry server', () => {
    const tunnel = { listen: '', sni: 'x.com', target: 'x.com:443' }
    const o = setupOptions({ entries: [{ key: 'n', line: { name: '', address: 'a', port: 1, enabled: true, entry_panel_id: 5, mode: 'tunnel' }, tunnel }], plan: null, loopbacks: {}, linkPorts: {}, routes: [] })
    expect(o.tunnels).toEqual({ 5: tunnel })
  })
})
