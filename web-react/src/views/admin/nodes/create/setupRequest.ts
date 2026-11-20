import type { CreateInboundRequest, NodeSetupOptions, NodeSetupPlan } from '@/api/nodes'
import type { PanelType } from '@/api/servers'
import type { InboundFormState } from '../inbound/inboundModel'
import { inboundDocuments } from '../inbound/inboundDocuments'
import { canonicalProtocolName } from '../inbound/inboundSettings'
import type { SetupRelay } from './setupModel'

/**
 * The body of the setup preview and of the create request (they are the same, the create one carries the
 * reviewed plan's token in `setup.confirm`). `sort_order` is not sent: a new node is appended, and a position is
 * written only by the order endpoint, where the person places it afterwards.
 */
export function buildSetupRequest({ form, panelType, json, entries, direct, egress, options }: {
  form: InboundFormState
  panelType: PanelType
  json: boolean
  entries: readonly SetupRelay[]
  direct: boolean
  egress: number
  options: NodeSetupOptions
}): CreateInboundRequest {
  const managed = form.tls_cert_mode === 'psp_managed' && form.cert_id > 0
  return {
    panel_id: form.panel_id,
    display_name: form.display_name,
    server_address: form.server_address,
    flow: form.protocol === 'vless' ? form.vless_flow.trim() : '',
    region: form.region,
    tags: form.tags_text ? form.tags_text.split(',').map(s => s.trim()).filter(Boolean) : [],
    cert_source: managed ? 'psp_managed' : undefined,
    cert_id: managed ? form.cert_id : undefined,
    relays: entries.map(({ line }) => ({ ...line, name: line.name.trim(), address: line.address.trim(), domain: line.domain?.trim() })),
    hide_direct: !direct,
    egress_panel_id: egress,
    setup: options,
    inbound: {
      remark: form.display_name,
      enable: panelType === '3xui' ? form.enable : true,
      listen: form.listen,
      port: form.port,
      protocol: canonicalProtocolName(form.protocol),
      ...inboundDocuments(form, panelType, json),
      allocate: '',
      expiry_time: 0,
    },
  }
}

/**
 * What the review step lets the person change, folded into the options sent back: the loopback and link ports the
 * plan showed are kept unless edited (so the second request asks for exactly what was reviewed), and an entry's
 * tunnel listener fields go out keyed by its entry server.
 */
export function setupOptions({ entries, plan, loopbacks, linkPorts, routes }: {
  entries: readonly SetupRelay[]
  plan: NodeSetupPlan | null
  loopbacks: Record<number, number>
  linkPorts: Record<string, number>
  routes: NodeSetupOptions['routes']
}): NodeSetupOptions {
  const tunnels = Object.fromEntries(entries.flatMap(e => (e.line.mode === 'tunnel' && e.line.entry_panel_id && e.tunnel ? [[e.line.entry_panel_id, e.tunnel]] : [])))
  const shown = Object.fromEntries((plan?.bindings ?? []).flatMap(b => (b.loopback ? [[b.panel_id, b.loopback]] : [])))
  const shownLinks = Object.fromEntries((plan?.bindings ?? []).flatMap(b => (b.kind === 'link' && !b.reused ? [[`${b.peer_id}/${b.panel_id}`, b.port]] : [])))
  return { loopbacks: { ...shown, ...loopbacks }, tunnels, link_ports: { ...shownLinks, ...linkPorts }, routes }
}
